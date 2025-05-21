package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

const maxMemory = 10 << 20

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
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

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	r.ParseMultipartForm(maxMemory)

	thumbnail, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "something went wrong retrieving the form data", err)
		return
	}
	defer thumbnail.Close()

	mediaType, _, _ := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if mediaType == "" {
		respondWithError(w, http.StatusBadRequest, "Missing Content-Type for thumbnail", err)
		return
	}

	if mediaType != "image/jpeg" && mediaType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "wrong image type for thumbnail", err)
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

	// creates a 32-byte slice to hold data
	randBytes := make([]byte, 32)

	// fills the byte slice with random bytes.
	_, err = rand.Read(randBytes)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error creating thumbnail random ID", err)
		return
	}

	// encode random bytes into a URL-safe base64 string
	randomBase64String := base64.RawURLEncoding.EncodeToString(randBytes)
	assetPath := getAssetPath(randomBase64String, mediaType)
	assetDiskPath := cfg.getAssetDiskPath(assetPath)

	dst, err := os.Create(assetDiskPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to create file", err)
		return
	}
	defer dst.Close()
	_, err = io.Copy(dst, thumbnail)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error saving file", err)
		return
	}

	thumbnailURL := cfg.getAssetURL(assetPath)
	oldThumbnail := getAssetFromURL(*videoData.ThumbnailURL)
	videoData.ThumbnailURL = &thumbnailURL

	err = cfg.db.UpdateVideo(videoData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video data", err)
		return
	}

	// delete old URL from disk
	oldThumbnailPath := fmt.Sprintf("./assets/%s", oldThumbnail)
	err = os.Remove(oldThumbnailPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error removing old thumbnail", err)
		return
	}

	respondWithJSON(w, http.StatusOK, videoData)
}
