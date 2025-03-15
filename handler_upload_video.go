package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	// Set a upload limit
	const maxMemory = 1 << 30

	// Extract the videoID from the URL path parameters
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, 400, "Invalid ID", err)
		return
	}

	// Authenticate the user
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, 401, "Couldn't find JWT", err)
		return
	}
	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, 401, "Couldn't validate JWT", err)
		return
	}

	// Get the video metadata from the db
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, 500, "Couldn't find video", err)
		return
	}
	if video.UserID != userID {
		respondWithError(w, 401, "You're not the owner of this video", nil)
		return
	}

	// Parse the uploaded video file from the form data
	r.ParseMultipartForm(maxMemory)
	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, 400, "Couldn't parse form file", err)
		return
	}
	defer file.Close()

	// Validate the uploaded file to ensure it's an MP4 video
	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, 400, "Invalid Content-Type header", err)
		return
	}
	if mediaType != "video/mp4" {
		respondWithError(w, 400, "Invalid media type. Must be a mp4 video", nil)
	}

	// Save the uploaded file to a temporary file on disk
	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, 500, "Couldn't create a temp file on server", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, 500, "Couldn't copy video to temp file on server", err)
		return
	}

	// Get the temporary video ratio
	ratio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, 500, "Couldn't get video ratio", err)
		return
	}
	var ratioPrefix string
	switch ratio {
	case "16:9":
		ratioPrefix = "landscape"
	case "9:16":
		ratioPrefix = "portrait"
	case "other":
		ratioPrefix = "other"
	}

	// Reset the tempFile's file pointer to the beginning
	_, err = tempFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, 500, "Couldn't reset the tempFile's pointer to start", err)
		return
	}

	// Create a processed version of the video, open it and discard the original
	processedVideoPath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, 500, "Couldn't process video for fast start", err)
		return
	}
	defer os.Remove(processedVideoPath)

	processedVideo, err := os.Open(processedVideoPath)
	if err != nil {
		respondWithError(w, 500, "Couldn't open processed video", err)
		return
	}
	defer processedVideo.Close()

	// Put the object into S3
	fileKey := getAssetPath(mediaType)
	fileKey = filepath.Join(ratioPrefix, fileKey)
	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &fileKey,
		Body:        processedVideo,
		ContentType: &mediaType,
	})
	if err != nil {
		respondWithError(w, 500, "Couldn't put object into S3 bucket", err)
		return
	}

	// Update the VideoURL of the video record in db with bucket,key string
	bucketKeyString := fmt.Sprintf("%s,%s", cfg.s3Bucket, fileKey)
	video.VideoURL = &bucketKeyString
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, 500, "Couldn't update video info in server database", err)
		return
	}

	video, err = cfg.dbVideoToSignedVideo(video)
	if err != nil {
		respondWithError(w, 500, "Couldn't generate presigned URL", err)
		return
	}

	respondWithJSON(w, 200, video)
}

func getVideoAspectRatio(filepath string) (string, error) {
	// Create a exec.Command and set its Stdout to a bytes.Buffer
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filepath)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// Run the command
	err := cmd.Run()
	if err != nil {
		return "", err
	}
	// Unmarshal the bytes.Buffer.Bytes() to a json struct
	var videoInfo struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}
	err = json.Unmarshal(stdout.Bytes(), &videoInfo)
	if err != nil {
		return "", err
	}

	// Get width and height from json struct
	width := videoInfo.Streams[0].Width
	height := videoInfo.Streams[0].Height

	// Check ratio
	if width == 16*height/9 {
		return "16:9", nil
	} else if height == 16*width/9 {
		return "9:16", nil
	} else {
		return "other", nil
	}
}

func processVideoForFastStart(filepath string) (string, error) {
	processedFilePath := fmt.Sprintf("%s.processing", filepath)
	// Create a exec.Command
	cmd := exec.Command("ffmpeg", "-i", filepath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", processedFilePath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// Run it
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("error ffmpeg video file: %v", err)
	}

	// Check for errors
	fileInfo, err := os.Stat(processedFilePath)
	if err != nil {
		return "", fmt.Errorf("could not stat processed file: %v", err)
	}
	if fileInfo.Size() == 0 {
		return "", fmt.Errorf("processed file is empty")
	}

	return processedFilePath, nil
}

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	// Create a Presing client and use it to get presigned HTTP request for object
	presignClient := s3.NewPresignClient(s3Client)
	presignedReq, err := presignClient.PresignGetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expireTime))
	if err != nil {
		return "", fmt.Errorf("couldn't get object presigned HTTP request: %w", err)
	}

	// Get and return the .URL field
	return presignedReq.URL, nil
}

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	if video.VideoURL == nil {
		return video, nil
	}
	parts := strings.Split(*video.VideoURL, ",")
	if len(parts) < 2 {
		return video, nil
	}
	bucket := parts[0]
	key := parts[1]
	presignedUrl, err := generatePresignedURL(cfg.s3Client, bucket, key, time.Minute*5)
	if err != nil {
		return video, err
	}
	video.VideoURL = &presignedUrl
	return video, nil
}
