package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"

	"github.com/streamingfast/logging"
	"go.uber.org/zap"
)

var zlog, _ = logging.PackageLogger("tronutil", "github.com/streamingfast/firehose-tron/pkg/tronutil")

// WriteJSONToFile writes data to a JSON file with pretty formatting
func WriteJSONToFile(data interface{}, filename string) error {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling JSON: %w", err)
	}

	if err := ioutil.WriteFile(filename, jsonData, 0644); err != nil {
		return fmt.Errorf("writing JSON file: %w", err)
	}

	zlog.Info("wrote JSON to file", zap.String("filename", filename))
	return nil
}

// ReadJSONFromFile reads JSON data from a file into the provided interface
func ReadJSONFromFile(filename string, v interface{}) error {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("reading JSON file: %w", err)
	}

	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("unmarshaling JSON: %w", err)
	}

	return nil
}

// PrettyPrintJSON prints data as formatted JSON to stdout
func PrettyPrintJSON(data interface{}) error {
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling JSON: %w", err)
	}

	fmt.Println(string(jsonData))
	return nil
}
