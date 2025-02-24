package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/develpudu/bookmark-parser/parser"
	_ "github.com/mattn/go-sqlite3"
)

const (
	dbPath = "data/bookmarks.db"
)

// ensureDataDir creates the data directory if it doesn't exist
func ensureDataDir() error {
	dataDir := "data"
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			return fmt.Errorf("error creating data directory: %v", err)
		}
	}
	return nil
}

func main() {
	// Ensure data directory exists
	if err := ensureDataDir(); err != nil {
		log.Fatal(err)
	}

	// Define subcommands
	parseCmd := flag.NewFlagSet("parse", flag.ExitOnError)
	bookmarkFile := parseCmd.String("file", "", "Path to the Chrome bookmarks HTML file")

	searchCmd := flag.NewFlagSet("search", flag.ExitOnError)
	query := searchCmd.String("query", "", "Search query for bookmarks")

	validateCmd := flag.NewFlagSet("validate", flag.ExitOnError)

	exportCmd := flag.NewFlagSet("export", flag.ExitOnError)
	outputFile := exportCmd.String("output", "", "Path to save the exported bookmarks HTML file")

	if len(os.Args) < 2 {
		fmt.Println("expected 'parse', 'search', 'validate', or 'export' subcommands")
		os.Exit(1)
	}

	// Initialize database
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("Error opening database: %v", err)
	}
	defer db.Close()

	// Handle subcommands
	switch os.Args[1] {
	case "parse":
		parseCmd.Parse(os.Args[2:])
		if *bookmarkFile == "" {
			log.Fatal("Please provide a bookmark file path using -file flag")
		}
		if err := parseBookmarks(db, *bookmarkFile); err != nil {
			log.Fatalf("Error parsing bookmarks: %v", err)
		}

	case "search":
		searchCmd.Parse(os.Args[2:])
		if *query == "" {
			log.Fatal("Please provide a search query using -query flag")
		}
		if err := searchBookmarks(db, *query); err != nil {
			log.Fatalf("Error searching bookmarks: %v", err)
		}

	case "validate":
		validateCmd.Parse(os.Args[2:])
		fmt.Println("Validating bookmarks...")
		if err := parser.ValidateAndUpdateBookmarks(db); err != nil {
			log.Fatalf("Error validating bookmarks: %v", err)
		}
		fmt.Println("Validation complete")

	case "export":
		exportCmd.Parse(os.Args[2:])
		if *outputFile == "" {
			log.Fatal("Please provide an output file path using -output flag")
		}
		if err := parser.ExportValidBookmarks(db, *outputFile); err != nil {
			log.Fatalf("Error exporting bookmarks: %v", err)
		}
		fmt.Printf("Successfully exported valid bookmarks to %s\n", *outputFile)

	default:
		fmt.Println("expected 'parse', 'search', 'validate', or 'export' subcommands")
		os.Exit(1)
	}

}

func parseBookmarks(db *sql.DB, filePath string) error {
	// Initialize database schema
	if err := parser.InitDB(db); err != nil {
		return fmt.Errorf("error initializing database: %v", err)
	}

	// Parse bookmarks from file
	bookmarks, err := parser.ParseFile(filePath)
	if err != nil {
		return fmt.Errorf("error parsing bookmarks file: %v", err)
	}

	// Save bookmarks to database
	if err := parser.SaveBookmarks(db, bookmarks); err != nil {
		return fmt.Errorf("error saving bookmarks: %v", err)
	}

	fmt.Printf("Successfully imported %d bookmarks\n", len(bookmarks))
	return nil
}

func searchBookmarks(db *sql.DB, query string) error {
	rows, err := db.Query(`
		SELECT title, url, folder, is_dead, is_redirect
		FROM bookmarks
		WHERE title LIKE ? OR url LIKE ?
	`, "%"+query+"%", "%"+query+"%")
	if err != nil {
		return fmt.Errorf("error searching bookmarks: %v", err)
	}
	defer rows.Close()

	var found bool
	for rows.Next() {
		var b parser.Bookmark
		err := rows.Scan(&b.Title, &b.URL, &b.Folder, &b.Dead, &b.Redirect)
		if err != nil {
			return fmt.Errorf("error scanning bookmark: %v", err)
		}

		found = true
		fmt.Printf("\nTitle: %s\nURL: %s\nFolder: %s\n", b.Title, b.URL, b.Folder)
		if b.Dead {
			fmt.Println("Status: Dead link")
		}
		if b.Redirect {
			fmt.Println("Status: Redirects to another location")
		}
	}

	if !found {
		fmt.Println("No bookmarks found matching your query")
	}

	return nil
}
