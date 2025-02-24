# Bookmark Parser

A command-line tool to parse Google Chrome bookmarks and store them in a SQLite database for easy searching and management.

## Features

- Parse Chrome bookmarks HTML export file
- Store bookmarks in SQLite database
- Search bookmarks by title or URL
- Check for dead links and redirects
- Organize bookmarks by folders
- Generate validation reports for bookmark status
- Export bookmarks in different categories (valid, dead links, redirects)
- Track duplicate bookmarks
- Maintain folder structure during import/export

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

### Validating Bookmarks

Check for dead links and redirects in your bookmarks:

```bash
./bookmark-parser validate
```

This will generate a validation report in the `output/validation_report.txt` file with statistics about valid URLs, dead links, and redirects.

### Exporting Bookmarks

Export your bookmarks based on their status:

```bash
./bookmark-parser export
```

This will create three files in the `output` directory:
- `bookmarks.html`: Valid bookmarks that are working correctly
- `dead-links.html`: Bookmarks that are no longer accessible
- `redirects.html`: Bookmarks that redirect to different URLs

All exported files maintain the original folder structure and can be imported back into Chrome.

## Development

To build the project:

```bash
go build
```

## License

MIT License