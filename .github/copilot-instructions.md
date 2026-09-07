When performing a pull request code review, use the full repository context and
review the change across file boundaries rather than considering changed lines
in isolation.

For Go CLI changes, trace data from argument parsing through network I/O,
parsing, normalization, filtering, transformation, and output. Pay particular
attention to:

- malformed or adversarial remote input, bounded resource use, cancellation,
  timeouts, redirects, and partial failures;
- standards compliance and lossless handling of timestamps, URLs, identifiers,
  content, authors, tags, and attachments across every supported feed format;
- deterministic behavior with multiple feeds and jq expressions that emit
  zero, one, or multiple values;
- consistency among implementation, tests, help output, and README examples;
- behavior on Linux, macOS, and Windows, including errors and exit status.

Report concrete correctness, security, reliability, compatibility, and test
coverage issues. Do not omit a finding merely because the affected code is
small or already has a happy-path test.
