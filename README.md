# sprout

A fast, interactive CLI for generating Spring Boot projects directly from your terminal, powered by Spring Initializr.

```console
$ sprout new my-api --deps web,data-jpa,postgresql --java 21
Generating my-api from start.spring.io…
✓ Created my-api in ./my-api (10 files)
  Gradle - Groovy · Java 21 · Spring Boot 4.1.2 · Jar
  Dependencies: web, data-jpa, postgresql

Next steps:
  cd ./my-api
  ./gradlew bootRun
```

> **Note:** Sprout is an independent, community CLI client for the public
> [Spring Initializr](https://start.spring.io) API. It is **not** an official
> Spring project and is not affiliated with or endorsed by the Spring team or
> Broadcom. "Spring" and "Spring Boot" are trademarks of their respective owners.

## Contents

- [Why Sprout?](#why-sprout)
- [Installation](#installation)
- [Quick start](#quick-start)
- [Interactive mode](#interactive-mode)
- [Flag-based mode](#flag-based-mode)
- [Dependency search](#dependency-search)
- [Spring Boot versions and server options](#spring-boot-versions-and-server-options)
- [Presets](#presets)
- [Configuration](#configuration)
- [Cache and offline use](#cache-and-offline-use)
- [Shell completion](#shell-completion)
- [Safety guarantees](#safety-guarantees)
- [How Sprout uses Spring Initializr](#how-sprout-uses-spring-initializr)
- [Troubleshooting](#troubleshooting)
- [Development](#development)
- [Contributing](#contributing)
- [License](#license)

## Why Sprout?

[start.spring.io](https://start.spring.io) is great, but sometimes you just want a
project without leaving the terminal. Sprout gives you:

- **The full Initializr feature set.** Sprout reads the server's metadata at runtime,
  so it offers every project type, language, Spring Boot version, Java version,
  packaging, configuration format and dependency the server offers. Nothing is
  hardcoded, so new Spring Boot releases and dependencies show up automatically.
- **A keyboard-first wizard** with searchable, multi-select dependencies, and a
  **flag mode** for scripts and CI, with the same options in both.
- **Validation before download.** Unknown dependency IDs, incompatible
  dependency/Boot combinations and unsupported values are caught locally, with
  suggestions (`did you mean data-jpa?`).
- **Safe, atomic project creation.** The archive is downloaded and extracted in a
  temporary directory, checked for path-traversal attacks, and moved into place
  only when complete. Existing files are never silently overwritten.
- **Presets, defaults, history, Git and editor integration.**
- **A single static binary** for Linux, macOS and Windows. No Java needed to
  generate a project.

## Installation

### Prebuilt binaries

Download the archive for your platform from the
[releases page](https://github.com/maskedsyntax/sprout/releases), extract it and put
`sprout` (or `sprout.exe`) on your `PATH`. Builds are available for:

| OS      | amd64 | arm64 |
|---------|-------|-------|
| Linux   | ✓     | ✓     |
| macOS   | ✓     | ✓     |
| Windows | ✓     | ✓     |

```sh
# Example: Linux x86-64
VERSION=0.1.0
curl -LO https://github.com/maskedsyntax/sprout/releases/download/v${VERSION}/sprout_${VERSION}_linux_amd64.tar.gz
tar -xzf sprout_${VERSION}_linux_amd64.tar.gz sprout
sudo install sprout /usr/local/bin/
```

Each release includes a `checksums.txt` for verification (`sha256sum -c checksums.txt --ignore-missing`).

### With Go

Requires Go 1.26 or newer:

```sh
go install github.com/maskedsyntax/sprout/cmd/sprout@latest
```

### From source

```sh
git clone https://github.com/maskedsyntax/sprout
cd sprout
make build      # produces ./bin/sprout with version information
```

### Updating

Download a newer release, or re-run `go install …@latest`. Check your version with
`sprout --version`.

## Quick start

```sh
sprout new                                   # interactive wizard
sprout new my-api                            # wizard, name pre-filled
sprout new my-api --deps web,data-jpa,postgresql
sprout new my-api --language kotlin --build gradle-kotlin
sprout new my-api --group com.example --artifact my-api
sprout new my-api --java 21 --boot 4.1.x
sprout new my-api --preset rest-api
sprout new my-api --git --open
sprout new my-api --deps web --dry-run       # show what would happen
```

Check your environment at any time:

```console
$ sprout doctor
✓ Config         ~/.config/sprout/config.toml
✓ Server         https://start.spring.io (412ms, 206 dependencies, default Spring Boot 4.1.2)
✓ Cache          fresh, fetched 16m ago
✓ Java           21.0.11 ~/.sdkman/candidates/java/current/bin/java
✓ Git            2.54.0 /usr/bin/git
· Maven          not found (optional: Maven projects include ./mvnw)
· Gradle         not found (optional: Gradle projects include ./gradlew)
· Editor         not configured (set with 'sprout config set editor <cmd>' or $EDITOR)
```

## Interactive mode

Running `sprout new` with no configuration flags in a terminal starts the wizard:

1. Project name
2. Project type, language and Spring Boot version
3. Group ID, artifact ID, description, package name and project version
4. Packaging, Java version and configuration file format
5. Any additional options the server advertises
6. Dependencies: type `/` to search by name, ID or category, `space` to select;
   the description and Boot compatibility of the highlighted dependency are shown
7. Output directory
8. Review, then generate

Keys: `Enter`/`Tab` next · `Shift+Tab` back · `/` search · `Space` select ·
`Ctrl+C` cancel (nothing is written).

Defaults come from the server, overridden by your [configuration](#configuration)
and any flags or preset you pass. The artifact ID defaults to a slug of the name
(`My API` → `my-api`) and the package to `<group>.<artifact>` (`com.example.my_api`).
Only dependencies compatible with the chosen Spring Boot version are listed.
The review step validates the whole configuration and lists any problems.

- `--interactive` / `-i` forces the wizard even when flags are given.
- `--no-interactive` never prompts.
- When stdin/stdout is not a terminal (pipes, CI), Sprout never prompts and
  requires a project name.
- `NO_COLOR` and `--no-color` disable colors. Set `ACCESSIBLE=1` for Huh's
  screen-reader friendly prompt mode.

## Flag-based mode

Every project option has a flag, so anything you can do in the wizard can be scripted:

| Flag | Initializr field | Example |
|------|------------------|---------|
| `-b, --build` | `type` | `maven`, `gradle`, `gradle-kotlin`, or a type ID such as `maven-project`, `gradle-project-kotlin`, `maven-build` |
| `-l, --language` | `language` | `java`, `kotlin`, `groovy` |
| `--boot` | `bootVersion` | `4.1.2`, `4.1.x`, `4.1`, `latest`, `4.2.0-RC2` |
| `--java` | `javaVersion` | `21` |
| `-g, --group` | `groupId` | `com.example` |
| `-a, --artifact` | `artifactId` | `my-api` (defaults to the project name) |
| `-n, --name` | `name` | `"My API"` (defaults to the artifact ID) |
| `-d, --description` | `description` | `"Order service"` |
| `--package` | `packageName` | `com.example.orders` |
| `--project-version` | `version` | `1.0.0-SNAPSHOT` |
| `--packaging` | `packaging` | `jar`, `war` |
| `--config-format` | `configurationFileFormat` | `properties`, `yaml` |
| `--deps` | `dependencies` | `web,data-jpa` (comma-separated or repeated) |
| `--set key=value` | any other server option | `--set someOption=value` |

Run `sprout options` to see every option the configured server supports, with its
values and defaults.

Other `new` flags:

| Flag | Meaning |
|------|---------|
| `-o, --output DIR` | Project directory to create (default `<output-dir>/<artifact>`, where `output-dir` defaults to the current directory) |
| `--force` | Merge into a non-empty directory. Files that would be replaced are first moved to `DIR/.sprout-backup-<timestamp>/`; nothing is deleted |
| `--dry-run` | Print the resolved configuration, target directory and request URL; download and write nothing |
| `-p, --preset NAME` | Start from a preset |
| `--last` | Start from the last successfully generated configuration |
| `--git` / `--no-git` | Initialize a Git repository with an initial commit (or not, overriding config) |
| `--open` | Open the project in your editor (config `editor`, `$VISUAL`, then `$EDITOR`) |
| `--editor CMD` | Open with this editor command (implies `--open`) |
| `--format json` / `--json` | Machine-readable output (CLI output format, unrelated to `--config-format`) |
| `-i, --interactive` / `--no-interactive` | Force or forbid the wizard |

**Precedence:** server defaults < `config` defaults < `--last` < `--preset` < flags.
Dependencies from all layers are combined. Error messages say which layer a bad
value came from, for example `java "11" (from preset "legacy") is not available`.

Global flags work with every command: `--server URL`, `--offline` and `--no-color`.

### Scripting example

```sh
sprout new orders --build maven --java 21 --deps web,data-jpa,postgresql \
  --group com.acme --no-interactive --json | jq -r .dir
```

## Dependency search

```console
$ sprout deps search security
  security                    Spring Security · Security
  security-saml2              SAML 2.0 · Security
  spring-security-webauthn    WebAuthn for Spring Security · Security
  oauth2-client               OAuth2 Client · Security
  ...
```

```sh
sprout deps                          # all dependencies, grouped by category
sprout deps list --category web      # one category (case-insensitive, partial match)
sprout deps search postgres          # search ID, name, description and category
sprout deps info data-jpa            # description, compatibility, docs links
sprout deps --boot 4.0.x             # only dependencies compatible with Boot 4.0.x
sprout deps --boot 4.0.x --all       # include incompatible ones, marked
sprout deps --json                   # machine-readable
```

Unknown IDs get suggestions: `unknown dependency "data-jp"; did you mean data-jpa?`.

## Spring Boot versions and server options

```console
$ sprout versions
  4.2.0-SNAPSHOT     snapshot
  4.2.0-RC2          release candidate
  4.1.3-SNAPSHOT     snapshot
  4.1.2              stable             (default)
  4.0.10-SNAPSHOT    snapshot
  4.0.9              stable
$ sprout versions --stable --json
```

Versions are classified by Initializr's naming conventions: no qualifier (or
`.RELEASE`) is a stable GA release, `-M<n>` a milestone, `-RC<n>` a release candidate,
and `-SNAPSHOT` / `.BUILD-SNAPSHOT` a snapshot.

`--boot` accepts an exact version, a version line (`4.1.x` or `4.1`) or `latest`. A
line resolves to its newest GA release, falling back to the newest milestone/RC and
then the newest snapshot.

## Presets

Presets are named configurations stored in `presets.toml` in the config directory:

```toml
[presets.rest-api]
language = "java"
type = "maven-project"
packaging = "jar"
group = "com.example"
java = "21"
dependencies = ["web", "data-jpa", "postgresql"]
```

Supported keys: `type`, `language`, `boot`, `group`, `artifact`, `name`,
`description`, `package`, `version`, `packaging`, `java`, `config-format`,
`dependencies`, and an `[presets.<name>.extra]` table for server-specific options.
See [docs/presets.example.toml](docs/presets.example.toml).

```sh
sprout presets list                      # your presets and built-in examples
sprout presets show rest-api             # contents + validation against current metadata
sprout presets save rest-api --java 21 --deps web,validation,actuator
sprout presets save my-usual --from-last # from the last generated project
sprout presets use rest-api my-api       # same as: sprout new my-api --preset rest-api
sprout presets edit rest-api --java 25   # update fields (--deps replaces the list)
sprout presets edit rest-api             # open presets.toml in your editor
sprout presets delete rest-api
```

Sprout ships five examples you can use and customize: `minimal`, `rest-api`,
`web-jpa`, `postgres` and `secure-api`. They are ordinary presets: saving or
editing one with the same name overrides it, and deleting your version restores
the example.

Presets are validated against the current server metadata every time they are
used. If a preset refers to a Java version, Boot line or dependency that is no
longer offered, Sprout explains what is outdated and does not generate a project
with silently changed settings.

## Configuration

```sh
sprout config show
sprout config set default-language java
sprout config set default-build maven-project      # alias of default-type
sprout config set default-java 21
sprout config set default-boot 4.1.x               # follows new patch releases
sprout config set default-group com.example
sprout config set default-deps devtools            # added to every project
sprout config set default-editor hx                # alias of editor
sprout config set output-dir ~/code                # parent directory for new projects
sprout config set git true                         # always initialize Git
sprout config set server https://start.spring.io   # or your own Initializr instance
sprout config set cache-ttl 6h
sprout config get default-java
sprout config unset default-java
sprout config reset                                # asks first; --yes in scripts
sprout config path
```

Run `sprout config set --help` for the full list of keys.

### File locations

| | Linux / BSD | macOS | Windows |
|-|-------------|-------|---------|
| Config (`config.toml`, `presets.toml`) | `$XDG_CONFIG_HOME/sprout` (default `~/.config/sprout`) | `~/Library/Application Support/sprout` | `%AppData%\sprout` |
| Cache (metadata) | `$XDG_CACHE_HOME/sprout` (default `~/.cache/sprout`) | `~/Library/Caches/sprout` | `%LocalAppData%\sprout` |
| State (`history.json`) | `$XDG_STATE_HOME/sprout` (default `~/.local/state/sprout`) | config dir | config dir |

Override them with `SPROUT_CONFIG_DIR`, `SPROUT_CACHE_DIR` and `SPROUT_STATE_DIR`.
`SPROUT_SERVER` overrides the configured server (the `--server` flag wins over both).

### History

```sh
sprout history            # recently generated projects
sprout history --json
sprout new another --last # reuse the last configuration with a new name
sprout history clear      # forget history (project files are untouched)
```

History is stored only on your machine. **Sprout collects no analytics or
telemetry.** It talks only to the configured Initializr server.

## Cache and offline use

Metadata is cached per server so commands start instantly. Cached data counts as
fresh for 12 hours by default (`sprout config set cache-ttl 6h`). After that it is
revalidated with an `ETag` conditional request.

```sh
sprout cache status
sprout cache refresh   # fetch now
sprout cache clear
```

If the server cannot be reached, browsing commands (`deps`, `versions`, `options`,
`presets show`) fall back to cached metadata and print a warning. `--offline`
forces cache-only mode. **Generating a project always requires the server**:
the project archive is produced by Spring Initializr, so `sprout new` cannot work
offline. `--offline --dry-run` still lets you preview a configuration.

## Shell completion

Completions include dependency IDs (also inside comma-separated `--deps` lists),
Boot versions and lines, Java versions, languages, project types, packaging,
configuration formats, preset names and config keys. Values come from cached
metadata, so completion is fast.

```sh
# Bash (requires bash-completion)
sprout completion bash > ~/.local/share/bash-completion/completions/sprout

# Zsh
sprout completion zsh > "${fpath[1]}/_sprout"

# Fish
sprout completion fish > ~/.config/fish/completions/sprout.fish

# PowerShell (add to $PROFILE)
sprout completion powershell | Out-String | Invoke-Expression
```

Run `sprout completion <shell> --help` for details.

## Safety guarantees

- **No partial projects.** The archive is downloaded into a hidden temporary
  directory next to the destination, verified, extracted, and then moved into place
  with a single rename. Interrupted downloads, server errors, cancellation (`Ctrl+C`)
  and disk errors leave nothing behind.
- **No overwrites.** A non-empty destination is refused. With `--force`, Sprout
  merges into it. Every file that would be replaced is first moved to
  `.sprout-backup-<timestamp>/` inside the destination. If anything fails midway,
  all changes are rolled back. Sprout never writes through symbolic links in the
  destination, and it never deletes your files.
- **Archive hardening.** Entries with absolute paths, `..` segments, backslashes,
  drive letters or colons are rejected (ZIP Slip protection), as are symbolic
  links, device files and duplicate entries. Size and entry-count limits protect
  against ZIP bombs. Executable bits (`mvnw`, `gradlew`) are preserved.
- **No shell.** Git and editors are started directly with argument lists, so
  project names or paths cannot inject commands. Editor commands such as
  `code --wait` are split into arguments with shell-style quoting rules.

## How Sprout uses Spring Initializr

Sprout is a client of the documented
[Spring Initializr API](https://docs.spring.io/initializr/docs/current/reference/html/).

- Metadata is requested from the service root with
  `Accept: application/vnd.initializr.v2.3+json` (falling back to v2.2/v2.1).
  Every capability is parsed generically: `action` (project types, with their
  `build`/`dialect`/`format` tags), `single-select`, `text` and the
  `hierarchical-multi-select` dependency tree, including defaults.
- Dependency compatibility uses each dependency's `versionRange`
  (for example `[4.0.0,4.1.0-M1)`) with Initializr's version ordering
  (`M` < `RC` < `SNAPSHOT` < GA).
- Projects are generated with the endpoint the metadata gives for the chosen type
  (`action`, such as `/starter.zip` or `/pom.xml`, falling back to the HAL link).
  Build-file-only types (`maven-build`, `gradle-build`) write just that file.
- Requests send `User-Agent: Sprout/<version> (+https://github.com/maskedsyntax/sprout; <os>/<arch>)`.
- Any compatible Initializr instance works, including self-hosted ones under a
  path prefix: `sprout config set server https://initializr.internal.example.com/start`.
  Server-specific options appear in the wizard and in `sprout options`, and can be
  set with `--set`.

### Known limitations

- **The server is more lenient than Sprout.** start.spring.io silently replaces some
  invalid values (an unsupported `javaVersion`, an invalid `packageName`) with
  defaults. Sprout validates against metadata first and reports the problem
  instead. If the server still rejects a request, its error message is shown
  verbatim.
- **Compatibility is only as good as the metadata.** Sprout checks the
  `versionRange` the server publishes. Rules that exist only on the server are
  reported when generation fails.
- **Only capability types Sprout understands can be set.** These are `text`,
  `single-select`, `action` and the dependency tree. If a server advertises a new
  kind of capability, `sprout doctor` reports it and it cannot be set until Sprout
  supports it.
- **Build-file-only types** (`maven-build`, `gradle-build`) are exactly what the
  server offers. For example, start.spring.io has no Kotlin-DSL build-file-only type.
- **No offline generation**, as explained [above](#cache-and-offline-use).

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| `cannot reach start.spring.io` / `timed out` | Check your connection or proxy. Sprout honors `HTTPS_PROXY`/`HTTP_PROXY`/`NO_PROXY`. Browsing commands keep working from cache. |
| `did not return Initializr metadata` | The `server` URL is not an Initializr instance (or a proxy returned HTML). Check `sprout config get server`. |
| `dependency "x" requires Spring Boot …` | Choose a compatible `--boot` (see `sprout deps info x`) or drop the dependency. |
| `… is not available; choose one of: …` | The value is not offered by the server (any more). Run `sprout options`. |
| A preset stopped working | `sprout presets show <name>` lists outdated values. Fix them with `sprout presets edit`. |
| `destination exists … is not empty` | Use a different `--output`, or `--force` to merge (with backups). |
| Options look outdated | `sprout cache refresh`. |
| The wizard does not start | stdin and stdout must be a terminal. Use flags in scripts, or `-i` to force it. |
| `no editor configured` | `sprout config set editor "code --wait"` or set `$EDITOR`. |
| Colors are unreadable | Set `NO_COLOR=1` or pass `--no-color`. |

`sprout doctor` checks connectivity, configuration, cache and local tools. Java,
Maven, Gradle and Git are optional: generated projects include build wrappers.

## Development

Requirements: Go 1.26+. No Java is needed.

```sh
make build         # ./bin/sprout with version info via -ldflags
make test          # unit tests (offline; a fake Initializr server is used)
make test-race
make integration   # optional live test against start.spring.io
make lint          # staticcheck + golangci-lint
make vet fmt
make snapshot      # local GoReleaser build of all release archives
```

### Layout

```text
cmd/sprout/            entry point (signal handling, exit codes)
internal/cli/          Cobra commands and terminal output
internal/wizard/       interactive Huh form
internal/initializr/   HTTP client (metadata, generation, errors); no UI code
internal/metadata/     metadata model, version/range logic, dependency search
internal/project/      options, layering and validation → generation request
internal/generator/    download, safe extraction, atomic placement
internal/cache/        metadata cache with TTL, ETag and offline fallback
internal/config/       config.toml
internal/presets/      presets.toml and built-in examples
internal/history/      generated-project history
internal/platform/     OS directories, atomic writes, git/editor launching
internal/testutil/     fake Initializr server for tests
```

The metadata fixture in `internal/metadata/testdata` is a recorded start.spring.io
response. Refresh it with:

```sh
curl -H 'Accept: application/vnd.initializr.v2.3+json' https://start.spring.io \
  | python3 -m json.tool > internal/metadata/testdata/start-spring-io-v2.3.json
```

(Some tests assert specific versions from the fixture. Update them when refreshing it.)

### Releasing

Push a tag such as `v0.1.0`. The release workflow runs GoReleaser, which builds the
archives, `checksums.txt` and release notes. Homebrew cask publishing is configured in
`.goreleaser.yaml`, but upload is disabled until a tap repository and a
`HOMEBREW_TAP_TOKEN` secret exist.

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md). In short:

1. Open an issue for larger changes first.
2. Keep the Initializr client free of UI code and avoid hardcoding server-provided values.
3. Add tests (`httptest`-based, no network) and run `make vet lint test`.
4. Keep user-facing output concise and predictable.

## License

[MIT](LICENSE) © 2026 Aftaab Siddiqui
