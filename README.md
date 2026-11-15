# gl-dash

Every merge request waiting on you across gitlab.com and self-managed GitLab, in one terminal screen, with the failing job's first error line in the row.

![gl-dash showing merge requests from gitlab.com and a self-managed GitLab in one table, with failing job error lines under red rows](demo/demo.gif)

<sub>Recorded with [VHS](https://github.com/charmbracelet/vhs) from [`demo/demo.tape`](demo/demo.tape) against seeded fake servers. No real projects.</sub>

```sh
go install github.com/kova1max/gl-dash@latest   # needs Go 1.26+
gl-dash                                          # uses the hosts you logged in to with glab
```

Not using [glab](https://gitlab.com/gitlab-org/cli)? Set a token and go: `GL_DASH_TOKEN_GITLAB_COM=glpat-… gl-dash`.

## The problem

GitLab's merge request homepage is browser-only and shows one instance at a time. If you work on gitlab.com and a self-managed GitLab, finding what waits on you means two tabs, and finding out *why* a pipeline is red means three more clicks per merge request.

```text
Before: the browser, per host, per red merge request
  gitlab.com/dashboard/merge_requests
  gitlab.example.dev/dashboard/merge_requests
  → open the merge request → open its pipeline → open the failed job → scroll the log

After
  $ gl-dash
   Review requested (7)  │  Returned to you (2)  │  Your merge requests (4)
    PROJECT           TITLE                                   AUTHOR CI  APPR THR CONFL  UPD HOST
    acme/shop-api     Add idempotency keys to order creation  sam    ✗    1/2   2        25m gitlab.com
      ✗ test:unit · expected 200, got 500 (orders_test.go:88)
    platform/billing  Retry failed webhook deliveries         morgan ✗    1/2   1        50m gitlab.example.dev
      ✗ lint · declared and not used: ctx (handler.go:42)
    acme/web          Checkout: show delivery estimate        kim    ✓    1/1   0         1h gitlab.com
```

**Measured:** on the seeded demo data (2 hosts, 13 rows, 3 failed pipelines) with 200 ms of simulated latency per API call, `gl-dash --print` shows every row and all 3 error lines in **1.63 s** (median of 5 runs, 58 GET requests). The browser route to the same answer is **11 page loads**: one homepage per host, then merge request, pipeline and job log for each red row. Rerun it with `go run ./bench`; raw output is in [`bench/results.txt`](bench/results.txt). The page-load count is counted, not timed: how long 11 page loads take depends on you.

## What's in a row

| Column  | Meaning |
|---------|---------|
| PROJECT, TITLE, AUTHOR | as on GitLab |
| CI      | head pipeline: `✓` passed, `✗` failed, `●` running, `○` pending or manual, `⊘` canceled or skipped, `-` none |
| APPR    | approvals given / required (just the count when none are required) |
| THR     | unresolved threads |
| CONFL   | `yes` when the merge request has conflicts |
| UPD     | time since the last update; rows are sorted by it, newest first |
| HOST    | which GitLab the merge request lives on |

Under a red row, gl-dash fetches the first failed job (jobs allowed to fail are skipped), reads its log and shows the first line that looks like a real error: compiler and linter `file:line` output, failing Go tests, pytest, Jest, ESLint, Rust, Python tracebacks, shell `not found`, and generic `error:`/`Error:` lines. Runner boilerplate is ignored. When no line matches, it shows the runner's reason (`exit code 1`) instead of guessing; a wrong line is worse than none.

## Sections

Three built-in sections, named after the groups on GitLab's merge request homepage:

| Section | Shows |
|---------|-------|
| Review requested | you are a reviewer and have not finished your review |
| Returned to you | you are the author, it is not a draft, and a reviewer commented or requested changes |
| Your merge requests | you are the author |

Each section merges rows from all hosts and keeps 20 rows by default.

## Auth

gl-dash reads the host list from glab's config and finds a token per host in this order:

1. `GL_DASH_TOKEN_<HOST>`, e.g. `GL_DASH_TOKEN_GITLAB_EXAMPLE_DEV` for `gitlab.example.dev`. glab's own `GITLAB_TOKEN` also works for `GITLAB_HOST` (gitlab.com when unset).
2. `token:` for that host in gl-dash's config file.
3. `glab config get token --host <host>`, which also returns tokens glab keeps in the OS keyring. Recent glab versions store tokens there by default, so they are not in glab's config file.

A token needs the `read_api` scope. gl-dash only sends GET requests.

A host with a bad or missing token gets one red line naming the fix, and the other hosts still load:

```text
✗ gitlab.example.dev: token rejected (401); run `glab auth login --hostname gitlab.example.dev` or set GL_DASH_TOKEN_GITLAB_EXAMPLE_DEV
```

Hosts that appear in glab's config without any token (glab writes a `gitlab.com` entry even if you never use it) are skipped quietly and listed as `no token:` in the status line.

## Config

Optional. gl-dash reads `$GL_DASH_CONFIG`, else `$XDG_CONFIG_HOME/gl-dash/config.yml`, else `~/.config/gl-dash/config.yml`, or the file passed with `--config`.

```yaml
# If hosts is set, exactly these hosts are queried; otherwise every glab host is.
hosts:
  - host: gitlab.com
  - host: gitlab.example.dev
    api_host: gitlab.example.dev:8443   # default: host; also read from glab
    api_protocol: https                 # default: https; also read from glab
    # token: glpat-…                    # prefer the env var or glab

defaults:
  mrsLimit: 20          # rows per section, at most 100

mrSections:             # replaces the built-in sections
  - title: Review requested
    filters: reviewer:@me my-review-state:unreviewed,review_started,unapproved
  - title: Backend, not drafts
    filters: label:backend draft:false
    limit: 50
```

Filter keys:

| Key | Example | Notes |
|-----|---------|-------|
| `author`, `assignee`, `reviewer` | `reviewer:@me` | `@me` is the token's user on each host |
| `state` | `state:merged` | `opened` (default), `closed`, `merged`, `locked`, `all` |
| `draft` | `draft:false` | |
| `label` | `label:backend` | repeat for "has all of these labels" |
| `my-review-state` | `my-review-state:unreviewed` | your own review state as a reviewer |
| `review-state` | `review-state:requested_changes,reviewed` | any other reviewer's state |

Review states: `unreviewed`, `review_started`, `reviewed`, `requested_changes`, `approved`, `unapproved`.

## Keys

| Key | Action |
|-----|--------|
| `j` / `k`, `↓` / `↑` | next / previous merge request |
| `g` / `G` | first / last |
| `ctrl+d` / `ctrl+u` | half page down / up |
| `l` / `h`, `→` / `←`, `tab` / `shift+tab` | next / previous section |
| `/` | filter rows; matches project, title, author, host, `draft`, `conflict`, pipeline status and error text |
| `esc` | clear the filter |
| `o`, `enter` | open the merge request in your browser (`$BROWSER` if set) |
| `r` | refresh |
| `?` | help |
| `q` | quit |

`gl-dash --print` loads everything, prints it as plain text and exits, for scripts and quick checks.

## Why this instead of…

- **GitLab's merge request homepage**: browser-only and one instance per tab. It does not show why a pipeline failed.
- **[glab-tui](https://github.com/rcieri/glab-tui)**: a good GitLab TUI, but each session is scoped to one repo or group on one host. gl-dash has no repo context and runs from any directory.
- **[gh-dash](https://github.com/dlvhdr/gh-dash)**: the inspiration, and GitHub-only.
- **`glab mr list`**: one repo or group on one host per call.

## Coming from gh-dash

Same idea, same section shape (`title`, `filters`, `limit`, under `mrSections` instead of `prSections`) and gh-dash's default navigation keys (`j`/`k`, `h`/`l`, `/`, `o`, `r`, `?`, `q`). Filters use the keys above rather than GitHub search syntax. A config importer is planned.

## Limits

- Read-only. It cannot approve, comment or merge yet.
- Polls the REST API when you start it or press `r`; there is no live update.
- Child pipelines are not followed: a pipeline that failed only in a downstream pipeline shows as failed with no error line.
- Only the first 4 MiB of a job log is read.
- "Returned to you" and "Review requested" need the reviewer state API, which older self-managed GitLab versions may not return; reviewers without a state count as unreviewed.
- Review-state filters are applied after listing, so a section reads at most 5 pages of merge requests per host; matches beyond that are not shown.
- The error-line finder is a heuristic. If it picks the wrong line for your tooling, please open an issue with the log excerpt.

## Roadmap

- Write actions (approve, comment, retry pipeline)
- gh-dash config importer
- Preview pane and assigned issues
- Following child pipelines

## Development

```sh
go test ./...                     # unit and end-to-end tests against a fake GitLab
go run ./bench                    # the measurement above
go run ./demo/fakegitlab 127.0.0.1:18081=demo/data/gitlab.com.json 127.0.0.1:18082=demo/data/gitlab.example.dev.json &
GL_DASH_CONFIG=demo/config.yml go run .
vhs demo/demo.tape                # re-record the GIF
```

Tests never call a real GitLab; they run against `internal/fakegitlab` with seeded data.

## License

[MIT](LICENSE)
