# github-lang-rank

Rank GitHub languages by total bytes using the GitHub API and render a simple SVG chart.

## Usage

```bash
go run .
```

## Flags

- `-config` Path to config JSON (default: `config.json`)
- `-username` GitHub username or org (required)
- `-token` GitHub token (optional but avoids rate limits)
- `-output` Output path for SVG chart (default: `lang-rank.svg`)
- `-org` Treat `-username` as an org (default: false)
- `-include-forks` Include forked repositories
- `-include-archived` Include archived repositories
- `-top` Limit to top N languages (0 = all, remainder can be grouped as `Other`)
- `-show-other` Show aggregated `Other` when `-top` is used (default: true)
- `-exclude` Comma-separated languages to exclude (adds a note to the chart)

## Example

```bash
go run . -username my-org -org -include-archived -top 10 -output charts/langs.svg
```

## Config file

`config.json` is read by default. CLI flags override config values.

```json
{
  "username": "octocat",
  "token": "",
  "output": "charts/lang-rank.svg",
  "include_forks": false,
  "include_archived": false,
  "org": false,
  "show_other": false,
  "exclude": [
    "TeX"
  ],
  "top": 6
}
```

## Automation

GitHub Actions can update the chart weekly. The workflow uses the built-in
`GITHUB_TOKEN` to call the API and commits the updated SVG back to the repo.
