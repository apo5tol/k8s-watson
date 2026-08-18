# Commit Convention

## Commit Message Format

Commit messages must follow the Conventional Commits 1.0.0 specification:

```text
<type>[optional scope][optional !]: <description>

[optional body]

[optional footer(s)]
```

- A lowercase type, followed by `: `, is required.
- Use `feat` for new functionality and `fix` for bug fixes. Other appropriate
  types include `docs`, `test`, `refactor`, `perf`, `build`, `ci`, `style`, and
  `chore`.
- An optional noun in parentheses may identify the affected scope.
- A short description must immediately follow the colon and space.
- An optional free-form body must start after one blank line.
- Optional footers must start after one blank line and use Git trailer syntax,
  such as `Refs: #123`.
- Mark a breaking change with `!` immediately before `:` or with a
  `BREAKING CHANGE: <description>` footer. `BREAKING CHANGE` must be uppercase.

Examples:

```text
feat(chat): add conversation history
fix(kubectl): reject interactive commands
docs: document commit convention
feat(config)!: rename model environment variable
```

## Commit Scope

- Each commit must contain isolated changes within a single scope.
- Split unrelated changes, or changes affecting different scopes, into separate
  commits.

## Git Operation Rules

- Create a commit only when the user explicitly requests it.
- Only the user may be the commit author. Do not add an agent or any other party
  as an author or co-author, and do not override the author's name or email
  configured by the user.
- Do not run `git pull` or `git push`. The user is responsible for fetching and
  publishing changes.
- After creating commits, provide a brief summary that lists each commit and
  describes the changes it contains.
