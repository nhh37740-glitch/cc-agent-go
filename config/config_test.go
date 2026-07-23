package config

import "testing"

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
