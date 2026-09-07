package rssnip

import (
	"bytes"
	"context"
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
	return writeItemsWithCodeContext(context.Background(), out, items, code, raw, asJSON)
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
	return writeItemsWithCodeContext(context.Background(), out, items, code, raw, asJSON)
}

func writeItemsWithCodeContext(ctx context.Context, out io.Writer, items []Item, code *gojq.Code, raw, asJSON bool) error {
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	first := true
	if asJSON {
		if _, err := fmt.Fprint(out, "["); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	}
	err := writeItemValues(ctx, items, code, func(value any) error {
		if asJSON {
			if !first {
				if _, err := fmt.Fprint(out, ","); err != nil {
					return fmt.Errorf("write output: %w", err)
				}
			}
			first = false
			if err := encoder.Encode(value); err != nil {
				return fmt.Errorf("write output: %w", err)
			}
			return nil
		}
		if raw {
			if text, ok := value.(string); ok {
				if _, err := fmt.Fprintln(out, text); err != nil {
					return fmt.Errorf("write output: %w", err)
				}
				return nil
			}
		}
		if err := encoder.Encode(value); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if asJSON {
		if _, err := fmt.Fprint(out, "]\n"); err != nil {
			return fmt.Errorf("write output: %w", err)
		}
	}
	return nil
}

func writeItemValues(ctx context.Context, items []Item, code *gojq.Code, writeValue func(any) error) error {
	for _, item := range items {
		iter, err := applyQuery(ctx, code, item)
		if err != nil {
			return err
		}
		for {
			value, ok := iter.Next()
			if !ok {
				break
			}
			if err, ok := value.(error); ok {
				return fmt.Errorf("run --jq expression: %w", err)
			}
			if err := writeValue(value); err != nil {
				return err
			}
		}
	}
	return nil
}

func applyQuery(ctx context.Context, code *gojq.Code, item Item) (gojq.Iter, error) {
	if code == nil {
		return gojq.NewIter[any](item), nil
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
	return code.RunWithContext(ctx, input), nil
}
