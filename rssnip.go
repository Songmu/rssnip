package rssnip

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const cmdName = "rssnip"

type stringList []string

func (ss *stringList) String() string {
	return strings.Join(*ss, ",")
}

func (ss *stringList) Set(value string) error {
	*ss = append(*ss, value)
	return nil
}

// Run runs rssnip with the supplied command-line arguments.
func Run(ctx context.Context, argv []string, outStream, errStream io.Writer) (runErr error) {
	return run(ctx, argv, os.Stdin, outStream, errStream)
}

func run(ctx context.Context, argv []string, inStream io.Reader, outStream, errStream io.Writer) (runErr error) {
	fs := flag.NewFlagSet(
		fmt.Sprintf("%s (v%s rev:%s)", cmdName, version, revision), flag.ContinueOnError)
	fs.SetOutput(errStream)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: %s [options] [--url URL ...] [URL ...]\n\n", cmdName)
		fmt.Fprintln(fs.Output(), "Feed URLs may also be read one per line from standard input.")
		fmt.Fprintln(fs.Output(), "Options:")
		fs.PrintDefaults()
	}

	ver := fs.Bool("version", false, "display version")
	var urls stringList
	fs.Var(&urls, "url", "feed URL (repeatable)")
	sinceValue := fs.String("since", "", "include items on or after RFC3339 time or YYYY-MM-DD")
	untilValue := fs.String("until", "", "include items on or before RFC3339 time or YYYY-MM-DD")
	jqExpression := fs.String("jq", "", "apply a jq expression to each item")
	rawOutput := fs.Bool("r", false, "write string jq results without JSON quoting")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	if *ver {
		return printVersion(outStream)
	}
	stdinURLs, err := readStdinURLs(inStream)
	if err != nil {
		return fmt.Errorf("read feed URLs from standard input: %w", err)
	}
	urls = append(urls, fs.Args()...)
	urls = append(urls, stdinURLs...)
	if len(urls) == 0 {
		fs.Usage()
		return fmt.Errorf("at least one feed URL is required")
	}
	if *rawOutput && *jqExpression == "" {
		return fmt.Errorf("-r requires --jq")
	}

	since, err := parseTimeBound(*sinceValue, false)
	if err != nil {
		return fmt.Errorf("invalid --since: %w", err)
	}
	until, err := parseTimeBound(*untilValue, true)
	if err != nil {
		return fmt.Errorf("invalid --until: %w", err)
	}
	if since != nil && until != nil && since.After(*until) {
		return fmt.Errorf("--since must not be after --until")
	}
	code, err := compileQuery(*jqExpression)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	for _, feedURL := range urls {
		feedItems, err := fetchFeed(ctx, client, feedURL)
		if err != nil {
			return err
		}
		filtered := feedItems
		if since != nil || until != nil {
			filtered = feedItems[:0]
			for _, item := range feedItems {
				if withinPeriod(item, since, until) {
					filtered = append(filtered, item)
				}
			}
		}
		if err := writeItemsWithCodeContext(ctx, outStream, filtered, code, *rawOutput); err != nil {
			return err
		}
	}
	return nil
}

func readStdinURLs(in io.Reader) ([]string, error) {
	if file, ok := in.(*os.File); ok {
		info, err := file.Stat()
		if err != nil {
			return nil, nil
		}
		if info.Mode()&os.ModeCharDevice != 0 {
			return nil, nil
		}
	}

	scanner := bufio.NewScanner(in)
	const maxURLLineSize = 1 << 20
	scanner.Buffer(make([]byte, 4096), maxURLLineSize)
	var urls []string
	for scanner.Scan() {
		if url := strings.TrimSpace(scanner.Text()); url != "" {
			urls = append(urls, url)
		}
	}
	return urls, scanner.Err()
}

func printVersion(out io.Writer) error {
	_, err := fmt.Fprintf(out, "%s v%s (rev:%s)\n", cmdName, version, revision)
	return err
}
