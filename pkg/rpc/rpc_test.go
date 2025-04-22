package rpc

import (
	"github.com/seedfourtytwo/solana-exporter/pkg/slog"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	slog.Init()
	code := m.Run()
	os.Exit(code)
}
