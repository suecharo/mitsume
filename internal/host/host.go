// Package host は、通知メッセージに載せる host 識別子の解決ロジックを提供する。
// 解決順は docs/configuration.md § host 識別子 に従う。
package host

import (
	"fmt"
	"os"
)

// EnvKey は host 識別子の環境変数名。
const EnvKey = "MITSUME_HOST"

// Resolve は host 識別子を「configHost > $MITSUME_HOST > os.Hostname()」の順で
// 解決する。各段の空文字は無効値として次段へ落とす。全段で決まらなければ error。
func Resolve(configHost string) (string, error) {
	if configHost != "" {
		return configHost, nil
	}
	if v := os.Getenv(EnvKey); v != "" {
		return v, nil
	}
	h, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("host: os.Hostname failed: %w", err)
	}
	if h == "" {
		return "", fmt.Errorf("host: cannot resolve (all sources empty)")
	}

	return h, nil
}
