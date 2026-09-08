package rssnip

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/itchyny/gojq"
)

func writeItems(out io.Writer, items []Item, expression string, raw bool) error {
	code, err := compileQuery(expression)
	if err != nil {
		return err
	}
	return writeItemsWithCodeContext(context.Background(), out, items, code, raw)
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

func writeItemsWithCodeContext(ctx context.Context, out io.Writer, items []Item, code *gojq.Code, raw bool) error {
	return writeItemsWithCodeContextAndFeed(ctx, out, items, code, raw, false)
}

func writeItemsWithCodeContextAndFeed(ctx context.Context, out io.Writer, items []Item, code *gojq.Code, raw, withFeed bool) error {
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	err := writeItemValuesWithFeed(ctx, items, code, withFeed, func(value any) error {
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
	return nil
}

func writeItemValues(ctx context.Context, items []Item, code *gojq.Code, writeValue func(any) error) error {
	return writeItemValuesWithFeed(ctx, items, code, true, writeValue)
}

func writeItemValuesWithFeed(ctx context.Context, items []Item, code *gojq.Code, withFeed bool, writeValue func(any) error) error {
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		iter, err := applyQueryWithFeed(ctx, code, item, withFeed)
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
	return applyQueryWithFeed(ctx, code, item, true)
}

func applyQueryWithFeed(ctx context.Context, code *gojq.Code, item Item, withFeed bool) (gojq.Iter, error) {
	value, err := itemValue(item, withFeed)
	if err != nil {
		return nil, err
	}
	if code == nil {
		return gojq.NewIter[any](value), nil
	}
	return code.RunWithContext(ctx, value), nil
}

func itemValue(item Item, withFeed bool) (any, error) {
	data, err := json.Marshal(item)
	if err != nil {
		return nil, fmt.Errorf("encode item: %w", err)
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("prepare item: %w", err)
	}
	if !withFeed {
		delete(value, "_feed")
	}
	return value, nil
}
