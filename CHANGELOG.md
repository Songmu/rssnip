# Changelog

## [v0.0.2](https://github.com/Songmu/rssnip/compare/v0.0.1...v0.0.2) - 2026-09-12

- Fail fast on feed errors by @Songmu in https://github.com/Songmu/rssnip/pull/22
- Fetch feeds concurrently with per-host pacing by @Songmu with @Copilot in https://github.com/Songmu/rssnip/pull/21
- Add a public Fetch/Parse library interface by @Songmu with @Copilot in https://github.com/Songmu/rssnip/pull/24

## [v0.0.1](https://github.com/Songmu/rssnip/commits/v0.0.1) - 2026-09-10

- Bump codecov/codecov-action from 5 to 7 by @dependabot[bot] in https://github.com/Songmu/rssnip/pull/3
- Bump actions/setup-go from 5 to 7 by @dependabot[bot] in https://github.com/Songmu/rssnip/pull/2
- Bump actions/checkout from 4 to 7 by @dependabot[bot] in https://github.com/Songmu/rssnip/pull/1
- Implement initial rssnip feed slicing CLI by @Songmu in https://github.com/Songmu/rssnip/pull/4
- Consolidate Copilot instructions into AGENTS.md by @Songmu with @Copilot in https://github.com/Songmu/rssnip/pull/7
- Remove obsolete `--json` output option by @Songmu with @Copilot in https://github.com/Songmu/rssnip/pull/6
- Restructure tests: testdata fixtures, table tests, shared helpers by @Songmu with @Copilot in https://github.com/Songmu/rssnip/pull/10
- Accept feed URLs from standard input by @Songmu with @Copilot in https://github.com/Songmu/rssnip/pull/9
- Make feed metadata opt-in by @Songmu with @Copilot in https://github.com/Songmu/rssnip/pull/11
- Add bounded feed pagination support by @Songmu with @Copilot in https://github.com/Songmu/rssnip/pull/12
- Add feed discovery fallback for blog URLs by @Songmu with @Copilot in https://github.com/Songmu/rssnip/pull/13
- Remove --url option in favor of positional arguments and stdin by @Songmu in https://github.com/Songmu/rssnip/pull/14
- Add --updated date filtering option by @Songmu in https://github.com/Songmu/rssnip/pull/15
- Add WordPress feed pagination fallback by @Songmu in https://github.com/Songmu/rssnip/pull/16
- Stop ordered pagination after since boundary by @Songmu in https://github.com/Songmu/rssnip/pull/17
- Default --since to the last seven days by @Songmu in https://github.com/Songmu/rssnip/pull/18
- Use local time and half-open date bounds by @Songmu in https://github.com/Songmu/rssnip/pull/19
- Add bundled rssnip Agent Skill by @Songmu in https://github.com/Songmu/rssnip/pull/20
