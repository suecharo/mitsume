// Package host は通知に載せる host 名を決める。
package host

import (
	"fmt"
	"os"
)

// Resolve は設定 JSON の host が空でなければそれを、空なら OS の hostname を返す。
func Resolve(configHost string) (string, error) {
	if configHost != "" {
		return configHost, nil
	}
	h, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("host: os.Hostname failed: %w", err)
	}
	if h == "" {
		return "", fmt.Errorf("host: OS hostname is empty")
	}

	return h, nil
}
