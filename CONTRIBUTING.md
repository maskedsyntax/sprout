# Contributing to Sprout

Thanks for your interest in improving Sprout!

## Ground rules

- Sprout is an independent client of Spring Initializr. Don't add wording, logos or
  branding that suggests the Spring team made or endorses it.
- **Don't hardcode server-provided data.** Project types, languages, versions and
  dependencies must come from the metadata. Examples and tests may use the recorded
  fixture.
- Keep `internal/initializr` and `internal/metadata` free of terminal/UI code.
- Prefer the standard library. Discuss new dependencies in an issue first.
- Never add telemetry or network calls to services other than the configured
  Initializr server.
- Filesystem changes must stay non-destructive: stage, validate, then move into place.

## Workflow

```sh
git clone https://github.com/maskedsyntax/sprout && cd sprout
make test        # fast, offline
make vet lint    # static analysis
make integration # optional: talks to start.spring.io
```

- Write tests for new behavior. Use `internal/testutil.NewServer` to simulate Initializr.
  Unit tests must not need network access.
- Run `gofmt` (CI checks it).
- Keep commits focused and describe the user-visible effect in the message.
- For UI changes, include before/after terminal output in the pull request.

## Reporting bugs

Please include `sprout version`, `sprout doctor` output, the command you ran and
the full error. Add `--dry-run --json` output if the problem is about a generated
project's configuration.
