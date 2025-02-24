package parser

import (
	"fmt"
	"database/sql"
	"golang.org/x/net/html"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type Bookmark struct {
	ID       int64
	Title    string
	URL      string
	Folder   string
	Dead     bool
	Redirect bool
}

// InitDB creates the bookmarks table if it doesn't exist
func InitDB(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS bookmarks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			url TEXT NOT NULL,
			folder TEXT,
			is_dead BOOLEAN DEFAULT FALSE,
			is_redirect BOOLEAN DEFAULT FALSE
		)
	`)
	return err
}

// ParseFile reads the HTML bookmark file and returns a slice of bookmarks
func ParseFile(filePath string) ([]Bookmark, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return Parse(file)
}

// Parse reads the HTML content and extracts bookmarks
func Parse(r io.Reader) ([]Bookmark, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}

	var bookmarks []Bookmark
	var currentFolder string

	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "h3":
				// Update current folder
				if n.FirstChild != nil {
					currentFolder = n.FirstChild.Data
				}
			case "a":
				// Extract bookmark
				var url, title string
				for _, attr := range n.Attr {
					if attr.Key == "href" {
						url = attr.Val
						break
					}
				}
				if n.FirstChild != nil {
					title = n.FirstChild.Data
				}
				if url != "" && title != "" {
					bookmarks = append(bookmarks, Bookmark{
						Title:  strings.TrimSpace(title),
						URL:    url,
						Folder: currentFolder,
					})
				}
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}

	traverse(doc)
	return bookmarks, nil
}

// ValidateBookmark checks if a bookmark URL is dead or redirects
func ValidateBookmark(bookmark *Bookmark) error {
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 1 {
				bookmark.Redirect = true
			}
			return nil
		},
	}

	resp, err := client.Get(bookmark.URL)
	if err != nil {
		bookmark.Dead = true
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bookmark.Dead = true
	}

	return nil
}

// SaveBookmarks stores the parsed bookmarks in the database
func SaveBookmarks(db *sql.DB, bookmarks []Bookmark) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`
		INSERT INTO bookmarks (title, url, folder)
		VALUES (?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, b := range bookmarks {
		_, err = stmt.Exec(b.Title, b.URL, b.Folder)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit()
}

// ValidateAndUpdateBookmarks checks all bookmarks in the database and updates their status
func ValidateAndUpdateBookmarks(db *sql.DB) error {
	rows, err := db.Query("SELECT id, url FROM bookmarks")
	if err != nil {
		return fmt.Errorf("error querying bookmarks: %v", err)
	}
	defer rows.Close()

	// Create a channel to receive results
	type validationResult struct {
		id       int64
		dead     bool
		redirect bool
		err      error
	}
	results := make(chan validationResult)

	// Start workers to validate URLs
	workerCount := 10
	workQueue := make(chan struct{}, workerCount)
	for i := 0; i < workerCount; i++ {
		workQueue <- struct{}{}
	}

	// Process bookmarks
	go func() {
		for rows.Next() {
			var id int64
			var url string
			if err := rows.Scan(&id, &url); err != nil {
				results <- validationResult{err: err}
				continue
			}

			<-workQueue // Acquire worker
			bookmarkID, bookmarkURL := id, url // Create new variables in this scope
			go func() {
				defer func() { workQueue <- struct{}{} }() // Release worker

				bookmark := &Bookmark{ID: bookmarkID, URL: bookmarkURL}
				err := ValidateBookmark(bookmark)
				results <- validationResult{
					id:       bookmarkID,
					dead:     bookmark.Dead,
					redirect: bookmark.Redirect,
					err:      err,
				}
			}()
		}
		close(results)
	}()

	// Update database with results
	stmt, err := db.Prepare("UPDATE bookmarks SET is_dead = ?, is_redirect = ? WHERE id = ?")
	if err != nil {
		return fmt.Errorf("error preparing update statement: %v", err)
	}
	defer stmt.Close()

	var updateCount int
	for result := range results {
		if result.err != nil {
			return fmt.Errorf("error validating bookmark: %v", result.err)
		}

		_, err = stmt.Exec(result.dead, result.redirect, result.id)
		if err != nil {
			return fmt.Errorf("error updating bookmark status: %v", err)
		}
		updateCount++
	}

	return nil
}

// ExportValidBookmarks exports non-dead, non-redirecting bookmarks to a Chrome-compatible HTML file
func ExportValidBookmarks(db *sql.DB, outputPath string) error {
	rows, err := db.Query(`
		SELECT title, url, folder 
		FROM bookmarks 
		WHERE is_dead = FALSE AND is_redirect = FALSE
		ORDER BY folder, title
	`)
	if err != nil {
		return fmt.Errorf("error querying valid bookmarks: %v", err)
	}
	defer rows.Close()

	// Group bookmarks by folder
	bookmarksByFolder := make(map[string][]Bookmark)
	for rows.Next() {
		var b Bookmark
		if err := rows.Scan(&b.Title, &b.URL, &b.Folder); err != nil {
			return fmt.Errorf("error scanning bookmark: %v", err)
		}
		bookmarksByFolder[b.Folder] = append(bookmarksByFolder[b.Folder], b)
	}

	// Create output file
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("error creating output file: %v", err)
	}
	defer file.Close()

	// Write HTML header
	_, err = file.WriteString(`<!DOCTYPE NETSCAPE-Bookmark-file-1>
<!-- This is an automatically generated file.
     It will be read and overwritten.
     DO NOT EDIT! -->
<META HTTP-EQUIV="Content-Type" CONTENT="text/html; charset=UTF-8">
<TITLE>Bookmarks</TITLE>
<H1>Bookmarks</H1>
<DL><p>
`)
	if err != nil {
		return fmt.Errorf("error writing HTML header: %v", err)
	}

	// Write bookmarks by folder
	for folder, bookmarks := range bookmarksByFolder {
		if folder != "" {
			_, err = fmt.Fprintf(file, "    <DT><H3>%s</H3>\n    <DL><p>\n", folder)
			if err != nil {
				return fmt.Errorf("error writing folder header: %v", err)
			}
		}

		for _, b := range bookmarks {
			_, err = fmt.Fprintf(file, "        <DT><A HREF=\"%s\">%s</A>\n", b.URL, b.Title)
			if err != nil {
				return fmt.Errorf("error writing bookmark: %v", err)
			}
		}

		if folder != "" {
			_, err = file.WriteString("    </DL><p>\n")
			if err != nil {
				return fmt.Errorf("error writing folder footer: %v", err)
			}
		}
	}

	// Write HTML footer
	_, err = file.WriteString("</DL><p>\n")
	if err != nil {
		return fmt.Errorf("error writing HTML footer: %v", err)
	}

	return nil
}