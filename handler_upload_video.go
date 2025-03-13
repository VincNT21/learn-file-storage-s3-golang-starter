package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
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

	// Put the object into S3
	fileKey := ratioPrefix + "/" + getAssetPath(mediaType)
	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &fileKey,
		Body:        tempFile,
		ContentType: &mediaType,
	})
	if err != nil {
		respondWithError(w, 500, "Couldn't put object into S3 bucket", err)
		return
	}

	// Update the VideoURL of the video record in db
	videoUrl := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, fileKey)
	video.VideoURL = &videoUrl
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, 500, "Couldn't update video info in server database", err)
		return
	}

}

type VideoInfo struct {
	Streams []struct {
		Index              int    `json:"index"`
		CodecName          string `json:"codec_name,omitempty"`
		CodecLongName      string `json:"codec_long_name,omitempty"`
		Profile            string `json:"profile,omitempty"`
		CodecType          string `json:"codec_type"`
		CodecTagString     string `json:"codec_tag_string"`
		CodecTag           string `json:"codec_tag"`
		Width              int    `json:"width,omitempty"`
		Height             int    `json:"height,omitempty"`
		CodedWidth         int    `json:"coded_width,omitempty"`
		CodedHeight        int    `json:"coded_height,omitempty"`
		ClosedCaptions     int    `json:"closed_captions,omitempty"`
		FilmGrain          int    `json:"film_grain,omitempty"`
		HasBFrames         int    `json:"has_b_frames,omitempty"`
		SampleAspectRatio  string `json:"sample_aspect_ratio,omitempty"`
		DisplayAspectRatio string `json:"display_aspect_ratio,omitempty"`
		PixFmt             string `json:"pix_fmt,omitempty"`
		Level              int    `json:"level,omitempty"`
		ColorRange         string `json:"color_range,omitempty"`
		ColorSpace         string `json:"color_space,omitempty"`
		ColorTransfer      string `json:"color_transfer,omitempty"`
		ColorPrimaries     string `json:"color_primaries,omitempty"`
		ChromaLocation     string `json:"chroma_location,omitempty"`
		FieldOrder         string `json:"field_order,omitempty"`
		Refs               int    `json:"refs,omitempty"`
		IsAvc              string `json:"is_avc,omitempty"`
		NalLengthSize      string `json:"nal_length_size,omitempty"`
		ID                 string `json:"id"`
		RFrameRate         string `json:"r_frame_rate"`
		AvgFrameRate       string `json:"avg_frame_rate"`
		TimeBase           string `json:"time_base"`
		StartPts           int    `json:"start_pts"`
		StartTime          string `json:"start_time"`
		DurationTs         int    `json:"duration_ts"`
		Duration           string `json:"duration"`
		BitRate            string `json:"bit_rate,omitempty"`
		BitsPerRawSample   string `json:"bits_per_raw_sample,omitempty"`
		NbFrames           string `json:"nb_frames"`
		ExtradataSize      int    `json:"extradata_size"`
		Disposition        struct {
			Default         int `json:"default"`
			Dub             int `json:"dub"`
			Original        int `json:"original"`
			Comment         int `json:"comment"`
			Lyrics          int `json:"lyrics"`
			Karaoke         int `json:"karaoke"`
			Forced          int `json:"forced"`
			HearingImpaired int `json:"hearing_impaired"`
			VisualImpaired  int `json:"visual_impaired"`
			CleanEffects    int `json:"clean_effects"`
			AttachedPic     int `json:"attached_pic"`
			TimedThumbnails int `json:"timed_thumbnails"`
			NonDiegetic     int `json:"non_diegetic"`
			Captions        int `json:"captions"`
			Descriptions    int `json:"descriptions"`
			Metadata        int `json:"metadata"`
			Dependent       int `json:"dependent"`
			StillImage      int `json:"still_image"`
		} `json:"disposition"`
		Tags struct {
			Language    string `json:"language"`
			HandlerName string `json:"handler_name"`
			VendorID    string `json:"vendor_id"`
			Encoder     string `json:"encoder"`
			Timecode    string `json:"timecode"`
		} `json:"tags,omitempty"`
		SampleFmt      string `json:"sample_fmt,omitempty"`
		SampleRate     string `json:"sample_rate,omitempty"`
		Channels       int    `json:"channels,omitempty"`
		ChannelLayout  string `json:"channel_layout,omitempty"`
		BitsPerSample  int    `json:"bits_per_sample,omitempty"`
		InitialPadding int    `json:"initial_padding,omitempty"`
		Tags0          struct {
			Language    string `json:"language"`
			HandlerName string `json:"handler_name"`
			VendorID    string `json:"vendor_id"`
		} `json:"tags,omitempty"`
		Tags1 struct {
			Language    string `json:"language"`
			HandlerName string `json:"handler_name"`
			Timecode    string `json:"timecode"`
		} `json:"tags,omitempty"`
	} `json:"streams"`
}

func getVideoAspectRatio(filepath string) (string, error) {
	// Create a exec.Command and set its Stdout to a bytes.Buffer
	var content bytes.Buffer
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filepath)
	cmd.Stdout = &content
	// Run the command
	err := cmd.Run()
	if err != nil {
		return "", err
	}
	// Unmarshal the bytes.Buffer.Bytes() to a json struct
	var videoInfo VideoInfo
	err = json.Unmarshal(content.Bytes(), &videoInfo)
	if err != nil {
		return "", err
	}

	// Get width and height from json struct
	width := videoInfo.Streams[0].Width
	height := videoInfo.Streams[0].Height

	// Check ratio
	if width/height == 16/9 {
		return "16:9", nil
	} else if height/width == 16/9 {
		return "9:16", nil
	} else {
		return "other", nil
	}

}
