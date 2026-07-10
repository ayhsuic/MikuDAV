package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dhowden/tag"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WebDAVClient struct {
	BaseURL    string
	Username   string
	Password   string
	HTTPClient *http.Client
}

type PropfindResponse struct {
	XMLName  xml.Name          `xml:"multistatus"`
	Response []PropfindElement `xml:"response"`
}

type PropfindElement struct {
	Href     string    `xml:"href"`
	Propstat Propstat  `xml:"propstat"`
}

type Propstat struct {
	Prop Prop `xml:"prop"`
}

type Prop struct {
	GetLastModified string      `xml:"getlastmodified"`
	GetContentType  string      `xml:"getcontenttype"`
	GetContentLength string     `xml:"getcontentlength"`
	ResourceType    ResourceType `xml:"resourcetype"`
}

type ResourceType struct {
	Collection *struct{} `xml:"collection"`
}

func NewWebDAVClient(baseURL, username, password string) *WebDAVClient {
	return &WebDAVClient{
		BaseURL:  strings.TrimRight(baseURL, "/"),
		Username: username,
		Password: password,
		HTTPClient: &http.Client{
			Timeout: time.Minute * 5,
		},
	}
}

func (c *WebDAVClient) doRequest(method, urlPath string, body io.Reader, depthHeader bool) (*http.Response, error) {
	fullURL := c.BaseURL + "/" + strings.TrimLeft(urlPath, "/")
	req, err := http.NewRequest(method, fullURL, body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.Username, c.Password)
	if depthHeader {
		req.Header.Set("Depth", "1")
	}
	return c.HTTPClient.Do(req)
}

func (c *WebDAVClient) ListDir(dirPath string) ([]PropfindElement, error) {
	body := `<?xml version="1.0" encoding="utf-8"?>
<D:propfind xmlns:D="DAV:">
  <D:prop>
    <D:getlastmodified/>
    <D:getcontenttype/>
    <D:getcontentlength/>
    <D:resourcetype/>
  </D:prop>
</D:propfind>`
	resp, err := c.doRequest("PROPFIND", dirPath, strings.NewReader(body), true)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var propfind PropfindResponse
	if err := xml.NewDecoder(resp.Body).Decode(&propfind); err != nil {
		return nil, err
	}
	return propfind.Response, nil
}

func (c *WebDAVClient) GetFileContent(filePath string) ([]byte, error) {
	resp, err := c.doRequest("GET", filePath, nil, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (c *WebDAVClient) GetFileStream(filePath string, offset, length int64) (io.ReadCloser, error) {
	fullURL := c.BaseURL + "/" + strings.TrimLeft(filePath, "/")
	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.Username, c.Password)
	if length > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))
	} else if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func isAudioFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".mp3", ".flac", ".ogg", ".aac", ".m4a", ".wav", ".opus", ".wma":
		return true
	}
	return false
}

func getCoverPath(dir string) string {
	names := []string{"cover.jpg", "cover.png", "folder.jpg", "front.jpg", "album.jpg"}
	for _, name := range names {
		candidate := dir + "/" + name
		// 这里无法提前检测文件是否存在，需在扫描时尝试下载
		// 返回候选路径，由调用者决定是否可用
	}
	return ""
}

func copyCoverFromWebDAV(client *WebDAVClient, remotePath, localDir, prefix string) (string, error) {
	data, err := client.GetFileContent(remotePath)
	if err != nil {
		return "", err
	}
	ext := strings.ToLower(filepath.Ext(remotePath))
	localName := prefix + "_cover" + ext
	localPath := filepath.Join(localDir, localName)
	if err := os.MkdirAll(localDir, 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(localPath, data, 0644); err != nil {
		return "", err
	}
	return localName, nil
}

func extractAndCacheCover(client *WebDAVClient, dir, prefix string, m tag.Metadata) (string, error) {
	localDir := "data/covers"
	if pic := m.Picture(); pic != nil {
		img, format, err := image.Decode(bytes.NewReader(pic.Data))
		if err == nil {
			var ext string
			switch format {
			case "jpeg":
				ext = ".jpg"
			case "png":
				ext = ".png"
			default:
				return "", fmt.Errorf("unsupported cover format: %s", format)
			}
			localName := prefix + "_cover" + ext
			localPath := filepath.Join(localDir, localName)
			if err := os.MkdirAll(localDir, 0755); err != nil {
				return "", err
			}
			f, err := os.Create(localPath)
			if err != nil {
				return "", err
			}
			defer f.Close()
			switch format {
			case "jpeg":
				err = jpeg.Encode(f, img, nil)
			case "png":
				err = png.Encode(f, img)
			}
			if err != nil {
				return "", err
			}
			return localName, nil
		}
	}
	// 尝试同目录图片文件
	for _, name := range []string{"cover.jpg", "cover.png", "folder.jpg", "front.jpg", "album.jpg"} {
		remotePath := dir + "/" + name
		localName, err := copyCoverFromWebDAV(client, remotePath, localDir, prefix)
		if err == nil {
			return localName, nil
		}
	}
	return "", nil
}

func extractLyrics(client *WebDAVClient, dir, filePath string, m tag.Metadata) string {
	if lyrics := m.Lyrics(); lyrics != "" {
		return lyrics
	}
	base := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	lrcPath := dir + "/" + base + ".lrc"
	data, err := client.GetFileContent(lrcPath)
	if err == nil {
		return string(data)
	}
	return ""
}

func scanWorker(ctx context.Context, wg *sync.WaitGroup, client *WebDAVClient, dir string, pool *pgxpool.Pool, sem chan struct{}) {
	defer wg.Done()
	sem <- struct{}{}
	defer func() { <-sem }()

	items, err := client.ListDir(dir)
	if err != nil {
		fmt.Printf("扫描目录失败 %s: %v\n", dir, err)
		return
	}

	for _, item := range items {
		if item.Href == dir || item.Href == dir+"/" {
			continue
		}
		isDir := item.Propstat.Prop.ResourceType.Collection != nil
		if isDir {
			wg.Add(1)
			go scanWorker(ctx, wg, client, item.Href, pool, sem)
		} else if isAudioFile(item.Href) {
			processAudioFile(ctx, client, item, pool)
		}
	}
}

func processAudioFile(ctx context.Context, client *WebDAVClient, item PropfindElement, pool *pgxpool.Pool) {
	filePath := item.Href
	dir := filepath.Dir(filePath)
	lastModified, _ := time.Parse(time.RFC1123, item.Propstat.Prop.GetLastModified)
	fileSize := item.Propstat.Prop.GetContentLength

	// 下载文件前 256KB 用于解析标签
	rc, err := client.GetFileStream(filePath, 0, 256*1024)
	if err != nil {
		fmt.Printf("无法下载文件头 %s: %v\n", filePath, err)
		return
	}
	defer rc.Close()
	headerData, err := io.ReadAll(io.LimitReader(rc, 256*1024))
	if err != nil {
		fmt.Printf("读取文件头失败 %s: %v\n", filePath, err)
		return
	}
	reader := bytes.NewReader(headerData)

	m, err := tag.ReadFrom(reader)
	if err != nil {
		fmt.Printf("解析标签失败 %s: %v\n", filePath, err)
		// 使用文件名作为标题
		m = nil
	}

	title := filepath.Base(filePath)
	artist := "未知艺术家"
	album := ""
	trackNumber := 0
	discNumber := 1
	genre := ""
	lyrics := ""
	var duration float64
	var bitrate int
	var sampleRate int

	if m != nil {
		if t := m.Title(); t != "" {
			title = t
		}
		if a := m.Artist(); a != "" {
			artist = a
		}
		if al := m.Album(); al != "" {
			album = al
		}
		trackNumber, _ = m.Track()
		discNumber, _ = m.Disc()
		genre = m.Genre()
		duration = float64(m.Length()) / float64(time.Second)
		if props, ok := m.(tag.HasFileInfo); ok {
			info := props.FileInfo()
			if info != nil {
				bitrate = info.BitRate
				sampleRate = info.SampleRate
			}
		}
		lyrics = extractLyrics(client, dir, filePath, m)
	}

	coverPath, err := extractAndCacheCover(client, dir, title, m)
	if err != nil {
		coverPath = ""
	}

	// upsert artist
	var artistID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO artists (name) VALUES ($1)
		ON CONFLICT (name) DO UPDATE SET name=EXCLUDED.name
		RETURNING id
	`, artist).Scan(&artistID)
	if err != nil {
		fmt.Printf("艺术家入库失败 %s: %v\n", artist, err)
		return
	}

	// upsert album
	var albumID *int64
	if album != "" {
		var aid int64
		err = pool.QueryRow(ctx, `
			INSERT INTO albums (name, artist_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING
			RETURNING id
		`, album, artistID).Scan(&aid)
		if err == nil {
			albumID = &aid
		} else {
			// 如果冲突无返回，尝试查询
			err = pool.QueryRow(ctx, `SELECT id FROM albums WHERE name=$1 AND artist_id=$2`, album, artistID).Scan(&aid)
			if err == nil {
				albumID = &aid
			}
		}
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO songs (title, artist_id, album_id, track_number, disc_number, genre, lyrics, duration, bitrate, sample_rate, file_path, file_size, cover_path, last_modified)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (file_path) DO UPDATE SET
			title = EXCLUDED.title,
			artist_id = EXCLUDED.artist_id,
			album_id = EXCLUDED.album_id,
			track_number = EXCLUDED.track_number,
			disc_number = EXCLUDED.disc_number,
			genre = EXCLUDED.genre,
			lyrics = EXCLUDED.lyrics,
			duration = EXCLUDED.duration,
			bitrate = EXCLUDED.bitrate,
			sample_rate = EXCLUDED.sample_rate,
			file_size = EXCLUDED.file_size,
			cover_path = EXCLUDED.cover_path,
			last_modified = EXCLUDED.last_modified
	`, title, artistID, albumID, trackNumber, discNumber, genre, lyrics, duration, bitrate, sampleRate, filePath, fileSize, coverPath, lastModified)
	if err != nil {
		fmt.Printf("歌曲入库失败 %s: %v\n", filePath, err)
	}
}

func ScanLibrary(pool *pgxpool.Pool) error {
	webdavURL := os.Getenv("WEBDAV_URL")
	username := os.Getenv("WEBDAV_USERNAME")
	password := os.Getenv("WEBDAV_PASSWORD")
	if webdavURL == "" || username == "" || password == "" {
		return fmt.Errorf("WebDAV 配置缺失")
	}

	concurrencyStr := os.Getenv("SCAN_CONCURRENCY")
	concurrency := runtime.NumCPU()
	if concurrencyStr != "" {
		if c, err := strconv.Atoi(concurrencyStr); err == nil && c > 0 {
			concurrency = c
		}
	}

	client := NewWebDAVClient(webdavURL, username, password)
	ctx := context.Background()
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	wg.Add(1)
	go scanWorker(ctx, &wg, client, "/", pool, sem)
	wg.Wait()
	fmt.Println("扫描完成")
	return nil
}
