# DLM - Download Manager

A fast, cli download manager with queue support and persistent configuration.

## Features

- File downloads with configurable number of chunks
- Queue management for batch downloads
- Persistent configuration with sensible defaults
- Progress tracking with real-time statistics
- Resume support for interrupted downloads
- Completed downloads tracking

## Installation

### Download a prebuilt release

Grab the archive for your OS and architecture from the
[latest release](https://github.com/arash-jafarpour/dlm/releases/latest),
extract it, and put `dlm` on your `PATH`:

```bash
# example: Linux amd64
tar -xzf dlm_linux_amd64.tar.gz
sudo mv dlm /usr/local/bin/
```

Once installed, keep `dlm` up to date with the built-in updater:

```bash
dlm update
```

### Build from source

Requires Go 1.25 or newer:

```bash
git clone https://github.com/arash-jafarpour/dlm
cd dlm
make install   # go install + shell completion
```

Or build a local binary without installing it:

```bash
make build     # produces ./dlm
```

## Shell Completion

DLM ships with shell completion for bash, zsh, fish, and powershell.

`make build` automatically installs the completion script for your current
`$SHELL` (bash, zsh, or fish) into its standard completion directory. The
commands below set it up manually or for other shells.

Load completions for the current session:

```bash
# bash
source <(dlm completion bash)

# zsh
source <(dlm completion zsh)

# fish
dlm completion fish | source

# powershell
dlm completion powershell | Out-String | Invoke-Expression
```

Install completions so they load automatically in new sessions:

```bash
# bash
dlm completion bash > ~/.local/share/bash-completion/completions/dlm

# zsh
dlm completion zsh > "${fpath[1]}/_dlm"

# fish
dlm completion fish > ~/.config/fish/completions/dlm.fish
```

Completion covers commands and subcommands, and suggests valid keys for
`dlm config set` (including `true`/`false` for `insecure_skip_verify`).

## Quick Start

Download a single file:

```bash
dlm download url https://example.com/file.zip
```

Add URLs to queue and download them all:

```bash
dlm queue add https://example.com/file1.zip
dlm queue add https://example.com/file2.zip
dlm download queue
```

## Output Flags

Control the terminal output of any command:

```bash
# quiet: script/cron mode — errors only, nothing else printed
dlm download queue --quiet
dlm download url <url> -q

# keep status messages, disable the progress bar
# (e.g. CI logs or wide terminals where \r redraws are noisy)
dlm download queue --no-progress
```

Both work on every command. Independently of the flags, the progress bar
also auto-silences when stdout is not a terminal (pipes, redirects, cron).

### Interactive UI

Run `dlm` with no arguments: on a real terminal with a non-empty queue it
opens the interactive board — **one page listing every queued file** in
queue order. Nothing starts on its own; the board opens fully stopped:

- `space` on a row starts that single file (or pauses/resumes/retries it,
  depending on its state)
- `s` toggles the whole run: start all (flowing mode — `queue_jobs` files
  download at once, each completion pulls the next waiting file, exactly
  like `dlm download queue`) or, pressed again, stop all: active transfers
  wind down (partial `.part` files remain) and every unfinished job goes
  back to `· waiting for you`, the state the board opened in

Rows make the difference between "needs you" and "the app is on it"
visible:

```
  · waiting for you  FAB - 33.mkv      held — press space, or s for all
  ⠋ starting  FAB - 30.mkv             dispatched / waiting for a slot
  FAB - 31.mkv  ████████░░░░░░  41.2% 1.2 GB/2.9 GB  21.4 MB/s  ETA 1m12s
```

```
dlm                    # interactive board (alternate screen)
dlm --inline           # draw inline instead of the alternate screen
dlm | cat              # not a terminal: prints the snapshot instead
```

Only file names are shown, never the raw URLs; the completed tab lists
date, time, and file name.

Keys are scoped per page, so a control never acts on another page's rows.

- **Global**: `tab`/`←`/`→` switch tabs · `1`–`3` jump · `?` help · `q` quit
- **Downloads**: `space` start/pause · `s` start all / stop all ·
  `r` retry · `x` cancel · `j`/`k` select and scroll · `a` add URLs ·
  `d` delete (asks for confirmation) · `J`/`K` move down/up ·
  `o` edit the queue file
- **Completed**: `j`/`k` scroll · `d` delete the selected entry ·
  `c` clear the history (both ask for confirmation)
- **Settings**: `j`/`k` select · `enter` edit (or `space` to toggle a
  boolean) · `r` reset that value to its default · `R` reset everything ·
  `o` edit `config.json` by hand — resets ask for confirmation first

Download values are validated exactly like `dlm config set`, and a change
applies to the next file you start (no restart needed). Pausing cancels the
transfer in place: the partial `.part` file stays on disk, so resuming
continues from the exact byte. Quitting cancels the whole run the same way
(nothing is lost, everything resumes).

Destructive actions (removing a queued file, resetting settings, deleting
or clearing history) always show a yes/no confirmation; `y`/`enter`
accepts, `n`/`esc` cancels.

The completed tab lists date, time, file name, size, and duration for every
finished download. While transfers run, the footer shows the aggregate
speed with a small sparkline.

Queue edits apply immediately and are written straight to `queue.txt`
(the add line accepts several whitespace-separated URLs at once; deleting
or reordering updates the file too, and a session in `$EDITOR` is
reconciled on return).

The downloads page shows a window sized to the terminal; `j`/`k` scroll it
(`▲ N more above` / `▼ N more below` markers show what is out of view). The
completed list and settings body use the same window and width, so every
page adapts to the terminal size.

When stdout is not a terminal, or the queue is empty, `dlm` prints a plain
snapshot (queue, completed, downloads dir, settings) and exits. `dlm --help`
still lists every command.

## File Locations

DLM stores its files in standard system locations:

- Configuration: `~/.config/dlm/config.json`
- Queue file: `~/.config/dlm/queue.txt`
- Completed list: `~/.config/dlm/completed.txt`
- Lock file: `~/.config/dlm/dlm.lock`
- Downloads: `~/Downloads/dlm/`

All paths are customizable via the config command.

## Commands

### Download

Download a single URL:

```bash
dlm download url <url>
```

Download all URLs from the queue:

```bash
dlm download queue
```

Show where downloads are saved:

```bash
dlm download path
```

### Queue Management

Add a URL to the queue:

```bash
dlm queue add https://example.com/file.zip
```

List all queued URLs:

```bash
dlm queue list
```

Clear the queue:

```bash
dlm queue clear
```

Show queue file location:

```bash
dlm queue path
```

Direct Queue File Editing

Instead of adding URLs one by one with dlm queue add, you can edit the queue file directly. This is useful when you have many URLs to add at once.
Find your queue file location:

```bash
dlm queue path
# Output: /home/user/.config/dlm/queue.txt
```

Open the file in your text editor and add one URL per line:

```txt
https://example.com/file1.zip
https://example.com/file2.tar.gz
https://example.com/file3.pdf
https://example.com/video.mp4
https://example.com/dataset.tar.gz
```

Then download all at once:

```bash
dlm download queue
```

Example workflow:

```bash
# Open queue file in your editor
nano ~/.config/dlm/queue.txt

# Or use echo to append multiple URLs
cat >> ~/.config/dlm/queue.txt << EOF
https://example.com/file1.zip
https://example.com/file2.tar.gz
https://example.com/file3.pdf
EOF

# Verify what's in the queue
dlm queue list

# Download everything
dlm download queue
```

This method is especially convenient when:

- Copying multiple URLs from a webpage or document
- Processing URLs from a script or automation tool
- Bulk-adding downloads from a list

### Completed Downloads

Clear the completed downloads list:

```bash
dlm completed clear
```

Show completed file location:

```bash
dlm completed path
```

### Configuration

Show current configuration:

```bash
dlm config show
```

Set a configuration value:

```bash
dlm config set num_chunks 16
dlm config set output_dir ~/my-downloads
dlm config set insecure_skip_verify true
```

Show config file location:

```bash
dlm config path
```

Reset to default configuration:

```bash
dlm config reset
```

### Version

Show the current version and build metadata:

```bash
dlm version
dlm --version
```

### Update

Update `dlm` in place to the latest GitHub release:

```bash
dlm update
```

Check whether a newer version exists without installing it:

```bash
dlm update --check
```

Self-update downloads the release asset matching your OS and architecture,
verifies its SHA256 checksum, and replaces the running binary. If `dlm` is
installed in a system directory, run the update with elevated permissions or
reinstall. Releases are published automatically by GoReleaser when a `v*` tag
is pushed.

## Configuration Options

| Key                    | Type     | Default                       | Description                               |
| ---------------------- | -------- | ----------------------------- | ----------------------------------------- |
| `num_chunks`           | `int`    | `8`                           | Number of parallel download chunks (1-16) |
| `queue_jobs`           | `int`    | `1`                           | Simultaneous queue downloads (1-8)        |
| `output_dir`           | `string` | `~/Downloads/dlm`             | Directory where files are saved           |
| `queue_file`           | `string` | `~/.config/dlm/queue.txt`     | Path to queue file                        |
| `completed_file`       | `string` | `~/.config/dlm/completed.txt` | Path to completed downloads list          |
| `insecure_skip_verify` | `bool`   | `false`                       | Skip TLS certificate verification         |

## Examples

### Basic Download

```bash
dlm download url https://releases.ubuntu.com/22.04/ubuntu-22.04.3-desktop-amd64.iso
```

### Batch Downloads

```bash
# Add multiple URLs
dlm queue add https://example.com/file1.zip
dlm queue add https://example.com/file2.tar.gz
dlm queue add https://example.com/file3.pdf

# Download all at once
dlm download queue
```

### Bulk Queue Management

```bash
# Copy multiple URLs from clipboard directly into queue file
dlm queue path  # Find the file location
nano ~/.config/dlm/queue.txt  # Edit with your favorite editor

# Or append from command line
echo "https://example.com/file1.zip" >> ~/.config/dlm/queue.txt
echo "https://example.com/file2.tar.gz" >> ~/.config/dlm/queue.txt

# Download all
dlm download queue
```

### Custom Configuration

```bash
# Use more chunks for faster downloads
dlm config set num_chunks 16

# Change download directory
dlm config set output_dir /mnt/storage/downloads

# Download with custom settings
dlm download url https://example.com/large-file.zip
```

### Working with Self-Signed Certificates

```bash
# Skip TLS verification (use with caution)
dlm config set insecure_skip_verify true
dlm download url https://internal-server.local/file.zip

# Re-enable verification
dlm config set insecure_skip_verify false
```

### Queue Workflow

```bash
# Build your download queue throughout the day
dlm queue add https://example.com/video1.mp4
dlm queue add https://example.com/video2.mp4
dlm queue add https://example.com/dataset.tar.gz

# Check what's queued
dlm queue list

# Download everything overnight
dlm download queue

# Clear completed items
dlm completed clear
```

## How It Works

DLM uses parallel chunked downloads to maximize throughput:

- HEAD request determines file size and server support for range requests
- File is split into configurable chunks (default: 8)
- Parallel downloads fetch each chunk simultaneously
- Chunks are assembled into the final file
- Progress is tracked in real-time with speed and ETA

If a download is interrupted, DLM can resume from where it left off (when the server supports range requests).

## Tips

- Increase chunks for faster downloads on high-bandwidth connections: `dlm config set num_chunks 16`
- Decrease chunks if you experience connection issues: `dlm config set num_chunks 4`
- Use the queue to batch downloads and run them when convenient
- Edit `queue.txt` directly when adding many URLs at once - just paste them in, one per line
- Check paths with `dlm config path`, `dlm queue path`, etc. to locate your files
- Reset config if something goes wrong: `dlm config reset`
