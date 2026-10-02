package config

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func FuzzNumericEnv(f *testing.F) {
	for _, value := range []string{"", "0", "1", "255", "256", "4294967296", "-1", "15m", "0s", "-1s"} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if strings.ContainsRune(value, '\x00') {
			t.Skip("environment variable values cannot contain NUL")
		}
		const key = "VAULT_CHAT_CONFIG_FUZZ_VALUE"
		t.Setenv(key, value)
		for _, bits := range []int{8, 32} {
			got, err := uintEnv(key, 2, bits)
			want, parseErr := strconv.ParseUint(value, 10, bits)
			if value == "" {
				want, parseErr = 2, nil
			}
			if parseErr != nil || want == 0 {
				if err == nil {
					t.Fatalf("uintEnv accepted invalid %q for %d bits", value, bits)
				}
			} else if err != nil || uint64(got) != want {
				t.Fatalf("uintEnv(%q, %d) = %d, %v; want %d", value, bits, got, err, want)
			}
		}
		got, err := durationEnv(key, time.Minute)
		want, parseErr := time.ParseDuration(value)
		if value == "" {
			want, parseErr = time.Minute, nil
		}
		if parseErr != nil || want <= 0 {
			if err == nil {
				t.Fatalf("durationEnv accepted invalid %q", value)
			}
		} else if err != nil || got != want {
			t.Fatalf("durationEnv(%q) = %v, %v; want %v", value, got, err, want)
		}
	})
}
