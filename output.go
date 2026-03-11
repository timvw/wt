package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

const (
	formatText = "text"
	formatJSON = "json"
)

var outputFormat = formatText

type jsonEnvelope struct {
	OK      bool   `json:"ok"`
	Command string `json:"command"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

func isJSONOutput() bool {
	return strings.EqualFold(strings.TrimSpace(outputFormat), formatJSON)
}

func validateOutputFormat() error {
	trimmed := strings.ToLower(strings.TrimSpace(outputFormat))
	switch trimmed {
	case formatText, formatJSON:
		outputFormat = trimmed
		return nil
	default:
		return fmt.Errorf("unsupported --format value %q (supported: text, json)", outputFormat)
	}
}

func commandPath(cmd *cobra.Command) string {
	if cmd == nil {
		return "wt"
	}
	return cmd.CommandPath()
}

func emitJSONSuccess(cmd *cobra.Command, data any) error {
	if !isJSONOutput() {
		return nil
	}
	return emitJSON(jsonEnvelope{OK: true, Command: commandPath(cmd), Data: data})
}

func emitJSONError(cmd *cobra.Command, err error) error {
	if !isJSONOutput() {
		return nil
	}
	return emitJSON(jsonEnvelope{OK: false, Command: commandPath(cmd), Error: err.Error()})
}

func emitJSON(payload jsonEnvelope) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(payload)
}
