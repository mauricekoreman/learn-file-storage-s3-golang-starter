package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

// 1 GB
const maxVideoSize = 1 << 30

const (
	Portrait  = "9:16"
	Landscape = "16:9"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid video ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading video", videoID, "by user", userID)

	r.Body = http.MaxBytesReader(w, r.Body, maxVideoSize)
	err = r.ParseMultipartForm(maxVideoSize)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "File size too big", err)
		return
	}

	videoData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Video not found", err)
		return
	}

	if videoData.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized for this video", err)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "something went wrong retrieving the form data", err)
		return
	}
	defer file.Close()

	mediaType, _, _ := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "wrong content type for video", err)
		return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error creating file", err)
		return
	}
	defer tempFile.Close()
	defer os.Remove(tempFile.Name())

	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error copying file", err)
		return
	}

	aspectRatio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get aspect ratio of video", err)
		return
	}

	_, err = tempFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error seeking to start of video", err)
		return
	}

	// creates a 32-byte slice to hold data
	randBytes := make([]byte, 32)

	// fills the byte slice with random bytes.
	_, err = rand.Read(randBytes)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error creating video random ID", err)
		return
	}

	// encode random bytes into a URL-safe base64 string
	randomBase64String := base64.RawURLEncoding.EncodeToString(randBytes)

	aspect := "portrait"
	if aspectRatio == Landscape {
		aspect = "landscape"
	}

	uploader := manager.NewUploader(cfg.s3Client)
	result, err := uploader.Upload(context.TODO(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(fmt.Sprintf("%s/%s", aspect, randomBase64String)),
		Body:        tempFile,
		ContentType: &mediaType,
	})

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error uploading video to server", err)
		return
	}

	videoData.VideoURL = &result.Location

	err = cfg.db.UpdateVideo(videoData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video data", err)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func getVideoAspectRatio(filePath string) (string, error) {
	var stdout bytes.Buffer
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	cmd.Stdout = &stdout

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("failed to run ffprobe: %w", err)
	}

	var result struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		}
	}

	if err = json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return "", fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	if len(result.Streams) == 0 {
		return "", fmt.Errorf("no streamsa found in video")
	}

	// Find the first video stream with width and height
	for _, stream := range result.Streams {
		if stream.Width > 0 && stream.Height > 0 {
			// Calculate greatest common divisor to simplify the ratio
			gcd := func(a, b int) int {
				for b != 0 {
					a, b = b, a%b
				}
				return a
			}

			divisor := gcd(stream.Width, stream.Height)
			calcWidth := stream.Width / divisor
			calcHeight := stream.Height / divisor

			return fmt.Sprintf("%d:%d", calcWidth, calcHeight), nil
		}
	}

	return "", fmt.Errorf("no video stream with valid dimensions found")
}
