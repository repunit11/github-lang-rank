# Plan

- Build a CLI that collects all repositories for a given GitHub user/org and clones them locally.
- Run LOC analysis per language (line counts) using a tool/library such as scc (or a pure Go fallback if needed).
- Aggregate totals into a ranked list and render a simple chart image (PNG/SVG).
- Provide CLI flags for username, token, output path, and filtering options (e.g., include/exclude forks, archived).
- Document usage and add a minimal example in README.
