package auth

import (
	"bytes"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func TestSetLogLevelFiltersEventsBelowThreshold(t *testing.T) {
	originalLevel := zerolog.GlobalLevel()
	originalLogger := log.Logger
	t.Cleanup(func() {
		zerolog.SetGlobalLevel(originalLevel)
		log.Logger = originalLogger
	})

	var output bytes.Buffer
	SetLogOutput(&output)
	if err := SetLogLevel("info"); err != nil {
		t.Fatalf("SetLogLevel: %v", err)
	}

	log.Debug().Msg("debug should be filtered")
	log.Info().Msg("info should be retained")

	if bytes.Contains(output.Bytes(), []byte("debug should be filtered")) {
		t.Error("debug event was written despite the info threshold")
	}
	if !bytes.Contains(output.Bytes(), []byte("info should be retained")) {
		t.Error("info event was filtered despite the info threshold")
	}
}

func TestSetLogLevel(t *testing.T) {
	original := zerolog.GlobalLevel()
	t.Cleanup(func() {
		zerolog.SetGlobalLevel(original)
	})

	tests := []struct {
		name  string
		level string
		want  zerolog.Level
	}{
		{name: "trace", level: "trace", want: zerolog.TraceLevel},
		{name: "debug", level: "debug", want: zerolog.DebugLevel},
		{name: "info", level: "info", want: zerolog.InfoLevel},
		{name: "warn", level: "warn", want: zerolog.WarnLevel},
		{name: "error", level: "error", want: zerolog.ErrorLevel},
		{name: "trim and lowercase", level: "  DEBUG ", want: zerolog.DebugLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := SetLogLevel(tt.level); err != nil {
				t.Fatalf("SetLogLevel(%q): %v", tt.level, err)
			}
			if got := zerolog.GlobalLevel(); got != tt.want {
				t.Errorf("GlobalLevel() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetLogLevel_RejectsUnsupportedLevel(t *testing.T) {
	original := zerolog.GlobalLevel()
	t.Cleanup(func() {
		zerolog.SetGlobalLevel(original)
	})

	if err := SetLogLevel("verbose"); err == nil {
		t.Fatal("SetLogLevel should reject unsupported levels")
	}
	if got := zerolog.GlobalLevel(); got != original {
		t.Errorf("GlobalLevel() changed after invalid level: got %v, want %v", got, original)
	}
}

func TestInitLogging_DefaultsToInfo(t *testing.T) {
	originalLevel := zerolog.GlobalLevel()
	originalLogger := log.Logger
	t.Cleanup(func() {
		zerolog.SetGlobalLevel(originalLevel)
		log.Logger = originalLogger
	})
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := InitLogging(); err != nil {
		t.Fatalf("InitLogging: %v", err)
	}
	if got := zerolog.GlobalLevel(); got != zerolog.InfoLevel {
		t.Errorf("default GlobalLevel() = %v, want %v", got, zerolog.InfoLevel)
	}
}

func TestInitLoggingWithLevel_ConfiguresSelectedLevel(t *testing.T) {
	originalLevel := zerolog.GlobalLevel()
	originalLogger := log.Logger
	t.Cleanup(func() {
		zerolog.SetGlobalLevel(originalLevel)
		log.Logger = originalLogger
	})
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := InitLoggingWithLevel("debug"); err != nil {
		t.Fatalf("InitLoggingWithLevel: %v", err)
	}
	if got := zerolog.GlobalLevel(); got != zerolog.DebugLevel {
		t.Errorf("configured GlobalLevel() = %v, want %v", got, zerolog.DebugLevel)
	}
}
