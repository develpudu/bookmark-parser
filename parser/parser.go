package parser

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type Bookmark struct {
	ID          int64
	Title       string
	URL         string
	Folder      string
	Dead        bool
	Redirect    bool
	RedirectURL string
	Duplicate   bool
	DuplicateOf int64
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
			is_redirect BOOLEAN DEFAULT FALSE,
			redirect_url TEXT,
			is_duplicate BOOLEAN DEFAULT FALSE,
			duplicate_of INTEGER,
			FOREIGN KEY(duplicate_of) REFERENCES bookmarks(id)
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
				bookmark.RedirectURL = req.URL.String()
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

	if bookmark.Redirect && bookmark.RedirectURL == "" {
		bookmark.RedirectURL = resp.Request.URL.String()
	}

	return nil
}

// SaveBookmarks stores the parsed bookmarks in the database
func SaveBookmarks(db *sql.DB, bookmarks []Bookmark) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}

	// First, check for duplicates
	urlMap := make(map[string]int64)
	for _, b := range bookmarks {
		var existingID int64
		err := tx.QueryRow("SELECT id FROM bookmarks WHERE url = ?", b.URL).Scan(&existingID)
		if err == nil {
			urlMap[b.URL] = existingID
		}
	}

	stmt, err := tx.Prepare(`
		INSERT INTO bookmarks (title, url, folder, is_duplicate, duplicate_of)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, b := range bookmarks {
		if duplicateID, exists := urlMap[b.URL]; exists {
			_, err = stmt.Exec(b.Title, b.URL, b.Folder, true, duplicateID)
		} else {
			_, err = stmt.Exec(b.Title, b.URL, b.Folder, false, nil)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit()
}

// ValidateAndUpdateBookmarks checks all bookmarks in the database and updates their status
func ValidateAndUpdateBookmarks(db *sql.DB) error {
	// Start a transaction
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("error starting transaction: %v", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query("SELECT id, url FROM bookmarks")
	if err != nil {
		return fmt.Errorf("error querying bookmarks: %v", err)
	}
	defer rows.Close()

	// Create a channel to receive results
	type validationResult struct {
		id          int64
		dead        bool
		redirect    bool
		redirectURL string
		err         error
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
		defer close(results)
		for rows.Next() {
			var id int64
			var url string
			if err := rows.Scan(&id, &url); err != nil {
				results <- validationResult{err: err}
				continue
			}

			<-workQueue                        // Acquire worker
			bookmarkID, bookmarkURL := id, url // Create new variables in this scope
			go func() {
				defer func() { workQueue <- struct{}{} }() // Release worker

				bookmark := &Bookmark{ID: bookmarkID, URL: bookmarkURL}
				err := ValidateBookmark(bookmark)
				results <- validationResult{
					id:          bookmarkID,
					dead:        bookmark.Dead,
					redirect:    bookmark.Redirect,
					redirectURL: bookmark.RedirectURL,
					err:         err,
				}
			}()
		}
	}()

	// Prepare statement within transaction
	stmt, err := tx.Prepare("UPDATE bookmarks SET is_dead = ?, is_redirect = ?, redirect_url = ? WHERE id = ?")
	if err != nil {
		return fmt.Errorf("error preparing update statement: %v", err)
	}
	defer stmt.Close()

	// Statistics for the report
	var stats struct {
		total     int
		dead      int
		redirects int
		valid     int
	}

	for result := range results {
		if result.err != nil {
			return fmt.Errorf("error validating bookmark: %v", result.err)
		}

		_, err = stmt.Exec(result.dead, result.redirect, result.redirectURL, result.id)
		if err != nil {
			return fmt.Errorf("error updating bookmark status: %v", err)
		}

		// Update statistics
		stats.total++
		if result.dead {
			stats.dead++
		} else if result.redirect {
			stats.redirects++
		} else {
			stats.valid++
		}
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing transaction: %v", err)
	}

	// Generate report
	reportPath := "output/validation_report.txt"
	if err := os.MkdirAll("output", 0755); err != nil {
		return fmt.Errorf("error creating output directory: %v", err)
	}

	report := fmt.Sprintf(
		"Bookmark Validation Report\n"+
			"========================\n\n"+
			"Total URLs processed: %d\n"+
			"Valid URLs: %d\n"+
			"Dead links: %d\n"+
			"Redirects: %d\n",
		stats.total, stats.valid, stats.dead, stats.redirects)

	if err := os.WriteFile(reportPath, []byte(report), 0644); err != nil {
		return fmt.Errorf("error writing report: %v", err)
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

// ExportDeadBookmarks exports dead bookmarks to a Chrome-compatible HTML file
func ExportDeadBookmarks(db *sql.DB, outputPath string) error {
	rows, err := db.Query(`
		SELECT title, url, folder 
		FROM bookmarks 
		WHERE is_dead = TRUE
		ORDER BY folder, title
	`)
	if err != nil {
		return fmt.Errorf("error querying dead bookmarks: %v", err)
	}
	defer rows.Close()

	return exportBookmarksToHTML(rows, outputPath, "Dead Links")
}

// ExportRedirectBookmarks exports redirecting bookmarks to a Chrome-compatible HTML file
func ExportRedirectBookmarks(db *sql.DB, outputPath string) error {
	rows, err := db.Query(`
		SELECT title, url, folder, redirect_url
		FROM bookmarks 
		WHERE is_redirect = TRUE
		ORDER BY folder, title
	`)
	if err != nil {
		return fmt.Errorf("error querying redirecting bookmarks: %v", err)
	}
	defer rows.Close()

	return exportRedirectBookmarksToHTML(rows, outputPath)
}

// ExportAllBookmarks exports all bookmarks to separate HTML files based on their status
func ExportAllBookmarks(db *sql.DB, outputDir string) error {
	// Create output directory if it doesn't exist
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("error creating output directory: %v", err)
	}

	// Export valid bookmarks
	if err := ExportValidBookmarks(db, filepath.Join(outputDir, "bookmarks.html")); err != nil {
		return fmt.Errorf("error exporting valid bookmarks: %v", err)
	}

	// Export dead bookmarks
	if err := ExportDeadBookmarks(db, filepath.Join(outputDir, "dead-links.html")); err != nil {
		return fmt.Errorf("error exporting dead bookmarks: %v", err)
	}

	// Export redirecting bookmarks
	if err := ExportRedirectBookmarks(db, filepath.Join(outputDir, "redirects.html")); err != nil {
		return fmt.Errorf("error exporting redirecting bookmarks: %v", err)
	}

	return nil
}

// Helper function to export bookmarks to HTML
func exportBookmarksToHTML(rows *sql.Rows, outputPath, title string) error {
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
	_, err = file.WriteString(fmt.Sprintf(`<!DOCTYPE NETSCAPE-Bookmark-file-1>
<!-- This is an automatically generated file.
     It will be read and overwritten.
     DO NOT EDIT! -->
<META HTTP-EQUIV="Content-Type" CONTENT="text/html; charset=UTF-8">
<TITLE>%s</TITLE>
<H1>%s</H1>
<DL><p>
`, title, title))
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

// Helper function to export redirecting bookmarks to HTML
func exportRedirectBookmarksToHTML(rows *sql.Rows, outputPath string) error {
	// Group bookmarks by folder
	bookmarksByFolder := make(map[string][]struct {
		Bookmark
		RedirectURL string
	})

	for rows.Next() {
		var b Bookmark
		var redirectURL string
		if err := rows.Scan(&b.Title, &b.URL, &b.Folder, &redirectURL); err != nil {
			return fmt.Errorf("error scanning bookmark: %v", err)
		}
		bookmarksByFolder[b.Folder] = append(bookmarksByFolder[b.Folder], struct {
			Bookmark
			RedirectURL string
		}{b, redirectURL})
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
<TITLE>Redirecting Bookmarks</TITLE>
<H1>Redirecting Bookmarks</H1>
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
			_, err = fmt.Fprintf(file, "        <DT><A HREF=\"%s\">%s</A> (Redirects to: %s)\n",
				b.URL, b.Title, b.RedirectURL)
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
