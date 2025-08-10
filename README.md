# doc-converter

`doc-converter` is a command-line tool for fetching my blog, alain.apigban.com , extracting specific content using CSS selectors, and saving it as clean Markdown files. It's designed for my personal use, as I have a need to archive my blog in a structured, readable format.

## Features

*   **Batch Processing:** Convert multiple URLs from a single input file.
*   **Precise Content Extraction:** Use CSS selectors to target the exact content you need from a web page.
*   **Markdown with Frontmatter:** Outputs clean Markdown and automatically includes a YAML frontmatter block with metadata like source URL, page title, description, and retrieval time.
*   **Organized Output:** Each run creates a unique, timestamped directory to keep conversions organized and prevent overwrites.
*   **Flexible Configuration:** Use command-line flags or a `config.yaml` file for configuration, with flags taking precedence.

## Installation

Ensure you have the `doc-converter` binary in a directory included in your system's PATH.

## Usage

The primary command is `doc-converter convert`. It requires an input file of URLs and a CSS selector.

### 1. Create a URL List

First, create a text file (e.g., `urls.txt`) with one URL per line:

```
https://alain.apigban.com
https://alain.apigban.com/posts/homelab/09/netlify-02/
```

### 2. Run the Conversion

Execute the `convert` command, providing the path to your URL file and the CSS selector for the content you want to extract.

```bash
# Basic usage with full flags
doc-converter convert --file urls.txt --selector "#theme"

# Usage with shorthand flags
doc-converter convert -f urls.txt -s "#theme"

# Specify a custom output directory
doc-converter convert -f urls.txt -s "#theme" -o "my-docs"
```

### Command-Line Flags

| Flag | Shorthand | Description | Required | Default |
 |---|---|---|---|---|
 | `--file` | `-f` | Path to the text file containing URLs. | Yes | |
 | `--selector` | `-s` | CSS selector for the main content to extract. | Yes | |
 | `--output` | `-o` | Parent directory for the output files. | No | `output` |
 | `--config` | | Path to a custom configuration file. | No | |

## Configuration File

For convenience, you can define your settings in a `config.yaml` file. The tool will automatically search for and use a `config.yaml` file in the current directory.

**Example `config.yaml`:**

```yaml
file: "urls.txt"
selector: "div#main-content"
output: "output"
```

With this file in place, you can run the tool without any flags:

```bash
doc-converter convert
```

### Configuration Precedence

Command-line flags will always override settings from the `config.yaml` file. This allows for quick, one-off changes.

```bash
# Uses 'file' and 'output' from config.yaml, but overrides the selector
doc-converter convert --selector "body"
```

## Output Structure

The tool creates a new, timestamped directory for each run to avoid conflicts. The structure is as follows:

```
output/
└── 20250810175451/
    ├── overview_of_container_registry.md
    └── overview_of_functions.md
```

*   **`output/`**: The main output directory (or the one specified with `--output`).
*   **`20250810175451/`**: A unique directory for the run, named with a `YYYYMMDDHHMMSS` timestamp.
*   **`*.md`**: The converted Markdown files. The filename is a sanitized version of the web page's `<title>`.

### File Content

Each generated Markdown file includes a YAML frontmatter block with extracted metadata, followed by the converted content.

**Example: `overview_of_functions.md`**

```markdown
---
description: Deploying My Portfolio Website on Netlify
retrieved_at: "2025-08-10T18:58:20+04:00"
source: https://alain.apigban.com/posts/homelab/09/netlify-02/
title: Deploying My Portfolio Website on Netlify
---

Toha

[...]
```

## Disclaimer

This tool is provided for legitimate, personal use cases, such as archiving your own content. The author is not responsible for any misuse of this tool. Users are solely responsible for ensuring that their use of this script complies with all applicable laws, as well as the terms of service of any website they access. This tool should not be used to violate copyright law or any website's terms of service.

## License

This project is licensed under the MIT License - see the [LICENSE](./LICENSE) file for details.