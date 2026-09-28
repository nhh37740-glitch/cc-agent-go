package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigureApplicationLoggerWritesJSONToFileAndStandardError(t *testing.T) {
	oldLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	logFilePath := filepath.Join(t.TempDir(), "logs", "server.jsonl")
	var standardErrorOutput bytes.Buffer

	applicationLogFile, configureLoggerError :=
		configureApplicationLogger(logFilePath, &standardErrorOutput)
	if configureLoggerError != nil {
		t.Fatalf("configure application logger: %v", configureLoggerError)
	}

	slog.Info("logger test message",
		"component", "logger_test",
		"operation", "write")
	if syncLogFileError := applicationLogFile.Sync(); syncLogFileError != nil {
		t.Fatalf("sync application log file: %v", syncLogFileError)
	}
	if closeLogFileError := applicationLogFile.Close(); closeLogFileError != nil {
		t.Fatalf("close application log file: %v", closeLogFileError)
	}

	logFileJSON, readLogFileError := os.ReadFile(logFilePath)
	if readLogFileError != nil {
		t.Fatalf("read application log file: %v", readLogFileError)
	}

	for outputName, logOutput := range map[string]string{
		"log file":       string(logFileJSON),
		"standard error": standardErrorOutput.String(),
	} {
		if !strings.Contains(logOutput, `"msg":"logger test message"`) {
			t.Fatalf("%s does not contain JSON log message: %s", outputName, logOutput)
		}
		if !strings.Contains(logOutput, `"component":"logger_test"`) {
			t.Fatalf("%s does not contain component field: %s", outputName, logOutput)
		}
	}
}
