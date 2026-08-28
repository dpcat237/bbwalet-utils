package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dpcat237/bbwalet-utils/config"
)

func setValidEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_HTTP_TIMEOUT", "5s")
	t.Setenv("APP_WALLET_BASE_URL", "https://wallet.example")
	t.Setenv("APP_WALLET_API_TOKEN", "")
	t.Setenv("APP_OUTPUT_DIR", "out")
}

func TestLoad_ValidEnv_PopulatesSettings(t *testing.T) {
	setValidEnv(t)

	require.NoError(t, config.Load())
	require.Equal(t, 5*time.Second, config.Settings.HTTPTimeout)
	require.Equal(t, "https://wallet.example", config.Settings.WalletBaseURL)
	require.Equal(t, "out", config.Settings.OutputDir)
}

func TestLoad_TokenOptional_NoError(t *testing.T) {
	setValidEnv(t)

	require.NoError(t, config.Load())
	require.Empty(t, config.Settings.WalletAPIToken)
}

func TestLoad_MissingRequired_ReturnsError(t *testing.T) {
	setValidEnv(t)
	t.Setenv("APP_WALLET_BASE_URL", "")

	err := config.Load()
	require.Error(t, err)
	require.Contains(t, err.Error(), "APP_WALLET_BASE_URL")
}

func TestLoad_NonPositiveTimeout_ReturnsError(t *testing.T) {
	setValidEnv(t)
	t.Setenv("APP_HTTP_TIMEOUT", "0s")

	err := config.Load()
	require.Error(t, err)
	require.Contains(t, err.Error(), "positive duration")
}
