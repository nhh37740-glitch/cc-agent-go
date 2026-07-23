package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDeepSeekAPIKey(t *testing.T) {
	localConfigFilePath :=
		filepath.Join(t.TempDir(), "local.json")
	writeLocalConfigError := os.WriteFile(
		localConfigFilePath,
		[]byte(`{"deepseekApiKey":"key-from-local-file"}`),
		0o600,
	)
	if writeLocalConfigError != nil {
		t.Fatalf("write local config: %v", writeLocalConfigError)
	}

	t.Setenv("CC_AGENT_LOCAL_CONFIG", localConfigFilePath)
	t.Setenv("DEEPSEEK_API_KEY", "")

	applicationConfigLoadedFromFile := Load()
	if applicationConfigLoadedFromFile.ApiKey != "key-from-local-file" {
		t.Fatalf(
			"ApiKey loaded from file = %q, want %q",
			applicationConfigLoadedFromFile.ApiKey,
			"key-from-local-file",
		)
	}

	t.Setenv("DEEPSEEK_API_KEY", "key-from-environment")

	applicationConfigLoadedFromEnvironment := Load()
	if applicationConfigLoadedFromEnvironment.ApiKey != "key-from-environment" {
		t.Fatalf(
			"ApiKey loaded from environment = %q, want %q",
			applicationConfigLoadedFromEnvironment.ApiKey,
			"key-from-environment",
		)
	}
}

func TestLoadMaximumParallelSubAgents(t *testing.T) {
	testCases := []struct {
		testName                 string
		configuredValue          string
		expectedMaximumSubAgents int
	}{
		{
			testName:                 "empty value uses default",
			configuredValue:          "",
			expectedMaximumSubAgents: 5,
		},
		{
			testName:                 "valid value is preserved",
			configuredValue:          "3",
			expectedMaximumSubAgents: 3,
		},
		{
			testName:                 "non integer uses default",
			configuredValue:          "three",
			expectedMaximumSubAgents: 5,
		},
		{
			testName:                 "zero uses default",
			configuredValue:          "0",
			expectedMaximumSubAgents: 5,
		},
		{
			testName:                 "negative value uses default",
			configuredValue:          "-1",
			expectedMaximumSubAgents: 5,
		},
		{
			testName:                 "value above hard limit uses default",
			configuredValue:          "6",
			expectedMaximumSubAgents: 5,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.testName, func(t *testing.T) {
			t.Setenv("MAX_PARALLEL_SUBAGENTS", testCase.configuredValue)

			applicationConfig := Load()

			if applicationConfig.MaximumParallelSubAgents != testCase.expectedMaximumSubAgents {
				t.Fatalf(
					"MaximumParallelSubAgents = %d, want %d",
					applicationConfig.MaximumParallelSubAgents,
					testCase.expectedMaximumSubAgents,
				)
			}
		})
	}
}

func TestLoadMaximumSubAgentRounds(t *testing.T) {
	testCases := []struct {
		testName              string
		configuredValue       string
		expectedMaximumRounds int
	}{
		{
			testName:              "empty value uses default",
			configuredValue:       "",
			expectedMaximumRounds: 50,
		},
		{
			testName:              "valid value is preserved",
			configuredValue:       "80",
			expectedMaximumRounds: 80,
		},
		{
			testName:              "non integer uses default",
			configuredValue:       "many",
			expectedMaximumRounds: 50,
		},
		{
			testName:              "zero uses default",
			configuredValue:       "0",
			expectedMaximumRounds: 50,
		},
		{
			testName:              "negative value uses default",
			configuredValue:       "-10",
			expectedMaximumRounds: 50,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.testName, func(t *testing.T) {
			t.Setenv(
				"MAXIMUM_SUBAGENT_ROUNDS",
				testCase.configuredValue,
			)

			applicationConfig := Load()

			if applicationConfig.MaximumSubAgentRounds !=
				testCase.expectedMaximumRounds {
				t.Fatalf(
					"MaximumSubAgentRounds = %d, want %d",
					applicationConfig.MaximumSubAgentRounds,
					testCase.expectedMaximumRounds,
				)
			}
		})
	}
}
