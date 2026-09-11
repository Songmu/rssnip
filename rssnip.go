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
	"sync"
	"time"
)

const (
	cmdName          = "rssnip"
	defaultSinceDays = 7
)

// Run runs rssnip with the supplied command-line arguments.
func Run(ctx context.Context, argv []string, outStream, errStream io.Writer) (runErr error) {
	return run(ctx, argv, os.Stdin, outStream, errStream)
}

func run(ctx context.Context, argv []string, inStream io.Reader, outStream, errStream io.Writer) (runErr error) {
	return runInLocation(ctx, argv, inStream, outStream, errStream, time.Local)
}

func runInLocation(
	ctx context.Context,
	argv []string,
	inStream io.Reader,
	outStream, errStream io.Writer,
	location *time.Location,
) (runErr error) {
	if len(argv) > 0 && argv[0] == "skills" {
		return runSkills(ctx, argv[1:], outStream, errStream)
	}

	startedAt := time.Now()
	fs := flag.NewFlagSet(
		fmt.Sprintf("%s (v%s rev:%s)", cmdName, version, revision), flag.ContinueOnError)
	fs.SetOutput(errStream)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: %s [options] [URL ...]\n\n", cmdName)
		fmt.Fprintln(fs.Output(), "Feed or blog/site URLs may be supplied as positional arguments")
		fmt.Fprintln(fs.Output(), "or one per line on standard input. Positional URLs are processed first.")
		fmt.Fprintln(fs.Output(), "Place options before URLs. HTML pages are searched for a feed link.")
		fmt.Fprintf(fs.Output(), "\nManage the bundled Agent Skill with '%s skills <command>'.\n", cmdName)
		fmt.Fprintln(fs.Output(), "Options:")
		fs.PrintDefaults()
	}

	ver := fs.Bool("version", false, "display version")
	sinceValue := fs.String("since", "", "include items on or after RFC3339 time or local YYYY-MM-DD (default: 7 days ago)")
	untilValue := fs.String("until", "", "include items before RFC3339 time or local YYYY-MM-DD")
	allItems := fs.Bool("all", false, "disable the default --since filter")
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

	sinceInput := *sinceValue
	if sinceInput == "" && *untilValue == "" && !*allItems {
		sinceInput = startedAt.Add(-defaultSinceDays * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	}
	if *allItems && (*sinceValue != "" || *untilValue != "") {
		return fmt.Errorf("--all cannot be combined with --since or --until")
	}
	since, err := parseTimeBound(sinceInput, location)
	if err != nil {
		return fmt.Errorf("invalid --since: %w", err)
	}
	until, err := parseTimeBound(*untilValue, location)
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

	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: newPoliteTransport(nil),
	}
	feedItemsByURL, err := fetchFeeds(
		ctx, client, urls, *maxPages, since, *preferUpdated)
	if err != nil {
		return err
	}
	for _, feedItems := range feedItemsByURL {
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

func fetchFeeds(
	ctx context.Context,
	client *http.Client,
	urls []string,
	maxPages int,
	since *time.Time,
	preferUpdated bool,
) ([][]Item, error) {
	items := make([][]Item, len(urls))
	errors := make([]error, len(urls))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(maxConcurrentFetches, len(urls)) {
		workers.Go(func() {
			for index := range jobs {
				items[index], errors[index] = fetchFeedPagesSince(
					ctx, client, urls[index], maxPages, since, preferUpdated)
			}
		})
	}
	for index := range urls {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	for _, err := range errors {
		if err != nil {
			return nil, err
		}
	}
	return items, nil
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
