package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

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

	// Handle a multipart form upload of a thumbnail image
	// Parse the form data from the request
	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)

	// Get image data from the multipart form
	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse form file", err)
		return
	}
	defer file.Close()

	// Get media type from file header (properly parsed by mime) and check if it's a image
	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid Content-Type", nil)
		return
	}

	if mediaType != "image/jpeg" && mediaType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Invalid media type. Must be a jpeg/png image", nil)
		return
	}

	// Get video metadata from db
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't find video in db", err)
		return
	}
	// Check if user is the current owner of the video
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Not authorized to update this video", nil)
		return
	}

	// Save thumbnail file to /assets directory on disk
	assetPath := getAssetPath(mediaType)
	assetDiskPath := cfg.getAssetDiskPath(assetPath)

	fileOnDisk, err := os.Create(assetDiskPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create new file on server for storing thumbnail", err)
		return
	}
	defer fileOnDisk.Close()

	_, err = io.Copy(fileOnDisk, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't copy thumbnail to file on disk", err)
		return
	}

	// Update the database
	thumbnailUrl := cfg.getAssetURL(assetPath)
	video.ThumbnailURL = &thumbnailUrl

	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video in db", err)
		return
	}

	// Respond
	respondWithJSON(w, http.StatusOK, video)
}
