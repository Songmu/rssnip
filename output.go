package rssnip

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/itchyny/gojq"
)

func writeItems(out io.Writer, items []Item, expression string, raw, asJSON bool) error {
	code, err := compileQuery(expression)
	if err != nil {
		return err
	}
	return writeItemsWithCode(out, items, code, raw, asJSON)
}

func compileQuery(expression string) (*gojq.Code, error) {
	if expression == "" {
		return nil, nil
	}
	query, err := gojq.Parse(expression)
	if err != nil {
		return nil, fmt.Errorf("parse --jq expression: %w", err)
	}
	code, err := gojq.Compile(query)
	if err != nil {
		return nil, fmt.Errorf("compile --jq expression: %w", err)
	}
	return code, nil
}

func writeItemsWithCode(out io.Writer, items []Item, code *gojq.Code, raw, asJSON bool) error {
	var results []any
	if asJSON {
		results = make([]any, 0, len(items))
	}
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	for _, item := range items {
		values, err := applyQuery(code, item)
		if err != nil {
			return err
		}
		for _, value := range values {
			if asJSON {
				results = append(results, value)
				continue
			}
			if raw {
				if text, ok := value.(string); ok {
					if _, err := fmt.Fprintln(out, text); err != nil {
						return fmt.Errorf("write output: %w", err)
					}
					continue
				}
			}
			if err := encoder.Encode(value); err != nil {
				return fmt.Errorf("write output: %w", err)
			}
		}
	}
	if asJSON {
		if err := encoder.Encode(results); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	}
	return nil
}

func applyQuery(code *gojq.Code, item Item) ([]any, error) {
	if code == nil {
		return []any{item}, nil
	}
	data, err := json.Marshal(item)
	if err != nil {
		return nil, fmt.Errorf("encode item: %w", err)
	}
	var input any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil {
		return nil, fmt.Errorf("prepare jq input: %w", err)
	}

	results := make([]any, 0, 1)
	iter := code.Run(input)
	for {
		value, ok := iter.Next()
		if !ok {
			break
		}
		if err, ok := value.(error); ok {
			return nil, fmt.Errorf("run --jq expression: %w", err)
		}
		results = append(results, value)
	}
	return results, nil
}
