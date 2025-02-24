# Bookmark Parser

A command-line tool to parse Google Chrome bookmarks and store them in a SQLite database for easy searching and management.

## Features

- Parse Chrome bookmarks HTML export file
- Store bookmarks in SQLite database
- Search bookmarks by title or URL
- Check for dead links and redirects
- Organize bookmarks by folders

## Installation

1. Ensure you have Go 1.21 or later installed
2. Clone this repository
3. Install dependencies:
   ```bash
   go mod download
   ```

## Usage

### Parsing Bookmarks

Export your Chrome bookmarks to an HTML file and use the parse command:

```bash
./bookmark-parser parse -file path/to/bookmarks.html
```

### Searching Bookmarks

Search through your parsed bookmarks:

```bash
./bookmark-parser search -query "search term"
```

The search will look for matches in both the title and URL of bookmarks.

## Development

To build the project:

```bash
go build
```

## License

MIT License