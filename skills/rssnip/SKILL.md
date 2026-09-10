---
name: rssnip
description: Use the rssnip CLI to fetch RSS, Atom, RDF, or JSON Feed entries as normalized JSON Lines. Use this skill whenever a task involves reading feeds or blog updates, discovering a feed from a site URL, filtering entries by publication or update time, combining multiple feeds, or transforming feed items with jq.
license: MIT
---

# rssnip

Use `rssnip` when feed or blog entries need to be fetched into a deterministic,
pipeline-friendly JSON Feed 1.1 item shape.

## Build the command

Put all options before the first URL:

```bash
rssnip [options] [URL ...]
```

Pass RSS, Atom, RDF/RSS, JSON Feed, or blog/site URLs. When given an HTML page,
`rssnip` discovers and fetches its first eligible feed link. Positional URLs
are processed first, followed by non-empty URL lines from standard input.
Duplicate input URLs are preserved.

By default, `rssnip` includes entries from the seven days before command
startup. Choose the time range deliberately:

```bash
# Include all available entries, subject to the pagination limit.
rssnip --all https://example.com/feed.xml

# Include January 2026 in the machine's local time zone.
rssnip --since 2026-01-01 --until 2026-02-01 \
  https://example.com/feed.xml

# Use portable UTC boundaries.
rssnip --since 2026-01-01T00:00:00Z --until 2026-02-01T00:00:00Z \
  https://example.com/feed.xml

# Prefer update times so recently revised entries are selected.
rssnip --since 2026-01-01 --updated https://example.com/feed.xml
```

`--since` is inclusive and `--until` is exclusive. Date-only bounds use the
machine's local time zone. Do not combine `--all` with either bound.

## Shape the output

Without `--jq`, each output line is one compact normalized JSON object.
Use `--with-feed` when entries from multiple feeds must retain source
attribution:

```bash
rssnip --with-feed \
  https://example.com/feed.xml \
  https://example.org/atom.xml
```

Use `--jq` to transform each item independently. The expression may emit zero,
one, or multiple values. Add `-r` only when string results should be written
without JSON quoting:

```bash
rssnip --jq '.url' -r https://example.com/feed.xml

rssnip --jq 'select(.tags | index("go")) | {title, url}' \
  https://example.com/feed.xml
```

For shell pipelines, preserve rssnip's non-zero exit status instead of treating
partial output as success.

## Control pagination and input

`rssnip` follows standard next-page links and fetches at most 10 pages per
input URL by default. Set a positive `--max-pages` when the task needs a
different bound:

```bash
rssnip --all --max-pages 3 https://example.com/feed.xml
```

Combine positional and standard-input URLs when useful:

```bash
printf '%s\n' https://example.org/feed.xml |
  rssnip --all https://example.com/feed.xml
```

This processes `example.com` before `example.org`. Output preserves feed, page,
and item order.

## Manage this skill

The rssnip binary ships this skill through skillsmith. Users explicitly manage
the installed copy:

```bash
rssnip skills list
rssnip skills install
rssnip skills status
rssnip skills update
```

The default destination is `~/.agents/skills`. Use `--scope repo` for
`<repo-root>/.agents/skills`, `--prefix` for a custom directory, and
`--dry-run` to preview changes. Use `--force` only when intentionally
overwriting an unmanaged skill or forcing a version change.
