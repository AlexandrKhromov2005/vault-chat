// Command s3-init provisions a bucket-scoped Media user using an isolated
// operator-only MinIO client. Only this job receives the root credentials.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	for _, key := range []string{"S3_ROOT_ACCESS_KEY", "S3_ROOT_SECRET_KEY", "S3_ACCESS_KEY", "S3_SECRET_KEY", "S3_ENDPOINT", "S3_BUCKET"} {
		if os.Getenv(key) == "" {
			return fmt.Errorf("%s is required", key)
		}
	}
	if os.Getenv("S3_ROOT_ACCESS_KEY") == os.Getenv("S3_ACCESS_KEY") {
		return fmt.Errorf("media must not use the root account")
	}
	bucket := os.Getenv("S3_BUCKET")
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`).MatchString(bucket) {
		return fmt.Errorf("invalid bucket name")
	}
	dir, err := os.MkdirTemp("", "vault-s3-init-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	policy := map[string]any{"Version": "2012-10-17", "Statement": []any{
		map[string]any{"Effect": "Allow", "Action": []string{"s3:GetBucketLocation", "s3:ListBucket", "s3:GetBucketPolicy"}, "Resource": []string{"arn:aws:s3:::" + bucket}},
		map[string]any{"Effect": "Allow", "Action": []string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject"}, "Resource": []string{"arn:aws:s3:::" + bucket + "/*"}},
	}}
	data, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	policyPath := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policyPath, data, 0600); err != nil {
		return err
	}
	commands := [][]string{
		{"alias", "set", "vault", os.Getenv("S3_ENDPOINT"), os.Getenv("S3_ROOT_ACCESS_KEY"), os.Getenv("S3_ROOT_SECRET_KEY")},
		{"mb", "--ignore-existing", "vault/" + bucket},
		{"anonymous", "set", "none", "vault/" + bucket},
		{"admin", "user", "add", "vault", os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_SECRET_KEY")},
		{"admin", "policy", "create", "vault", "vault-media", policyPath},
		{"admin", "policy", "attach", "vault", "vault-media", "--user", os.Getenv("S3_ACCESS_KEY")},
	}
	for _, args := range commands {
		cmd := exec.Command("/usr/local/bin/mc", append([]string{"--config-dir", dir}, args...)...)
		// mc output can include endpoint credentials; never forward it.
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("storage provisioning step %s failed: %w", args[0], err)
		}
	}
	return nil
}
