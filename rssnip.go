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

// Run runs rssnip with the supplied command-line arguments.
func Run(ctx context.Context, argv []string, outStream, errStream io.Writer) (runErr error) {
	return run(ctx, argv, os.Stdin, outStream, errStream)
}

func run(ctx context.Context, argv []string, inStream io.Reader, outStream, errStream io.Writer) (runErr error) {
	fs := flag.NewFlagSet(
		fmt.Sprintf("%s (v%s rev:%s)", cmdName, version, revision), flag.ContinueOnError)
	fs.SetOutput(errStream)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: %s [options] [URL ...]\n\n", cmdName)
		fmt.Fprintln(fs.Output(), "Feed or blog/site URLs may be supplied as positional arguments")
		fmt.Fprintln(fs.Output(), "or one per line on standard input. Positional URLs are processed first.")
		fmt.Fprintln(fs.Output(), "Place options before URLs. HTML pages are searched for a feed link.")
		fmt.Fprintln(fs.Output(), "Options:")
		fs.PrintDefaults()
	}

	ver := fs.Bool("version", false, "display version")
	sinceValue := fs.String("since", "", "include items on or after RFC3339 time or YYYY-MM-DD")
	untilValue := fs.String("until", "", "include items on or before RFC3339 time or YYYY-MM-DD")
	preferUpdated := fs.Bool("updated", false, "prefer the updated date over the published date when filtering by date")
	jqExpression := fs.String("jq", "", "apply a jq expression to each item")
	rawOutput := fs.Bool("r", false, "write string jq results without JSON quoting")
	withFeed := fs.Bool("with-feed", false, "include source feed information in each item")
	maxPages := fs.Int("max-pages", defaultMaxPages, "maximum pages to fetch from each feed")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	if *ver {
		return printVersion(outStream)
	}
	stdinURLs, err := readStdinURLs(inStream)
	if err != nil {
		return fmt.Errorf("read feed or blog/site URLs from standard input: %w", err)
	}
	urls := append([]string(nil), fs.Args()...)
	urls = append(urls, stdinURLs...)
	if len(urls) == 0 {
		fs.Usage()
		return fmt.Errorf("at least one feed or blog/site URL is required")
	}
	if *rawOutput && *jqExpression == "" {
		return fmt.Errorf("-r requires --jq")
	}
	if *maxPages < 1 {
		return fmt.Errorf("--max-pages must be at least 1")
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
	if *preferUpdated && since == nil && until == nil {
		return fmt.Errorf("--updated requires --since or --until")
	}
	code, err := compileQuery(*jqExpression)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	for _, feedURL := range urls {
		feedItems, err := fetchFeedPages(ctx, client, feedURL, *maxPages)
		if err != nil {
			return err
		}
		filtered := feedItems
		if since != nil || until != nil {
			filtered = feedItems[:0]
			for _, item := range feedItems {
				if withinPeriod(item, since, until, *preferUpdated) {
					filtered = append(filtered, item)
				}
			}
		}
		if err := writeItemsWithCodeContextAndFeed(ctx, outStream, filtered, code, *rawOutput, *withFeed); err != nil {
			return err
		}
	}
	return nil
}

func readStdinURLs(in io.Reader) ([]string, error) {
	if file, ok := in.(*os.File); ok {
		info, err := file.Stat()
		if err != nil {
			return nil, err
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
