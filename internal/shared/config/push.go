package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
)

// pushBrokerURL reads the URL of the push signal broker from key: empty, or
// tls://host:port without credentials, path or query. Services authenticate
// with their internal certificate, never with a password in the URL.
func pushBrokerURL(key string) (string, error) {
	value := os.Getenv(key)
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "tls" || parsed.User != nil || parsed.Opaque != "" ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("environment variable %s must be tls://host:port", key)
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil || host == "" || port == "" {
		return "", fmt.Errorf("environment variable %s must be tls://host:port", key)
	}
	return "tls://" + parsed.Host, nil
}
