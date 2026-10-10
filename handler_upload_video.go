package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	// Set memory
	const maxMemory = 10 << 30
	r.Body = http.MaxBytesReader(w, r.Body, maxMemory)

	// Validate
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

	// Get the video struct from SQLITE
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Invalid video ID", err)
		return
	}
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User is unauthorized to access this video", err)
		return
	}

	fmt.Println("uploading video", videoID, "by user", userID)

	// Upload sequence
	// Parse file from video key
	file, header, err := r.FormFile("video")
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			respondWithError(w, http.StatusRequestEntityTooLarge, "File too large", err)
			return
		}
		respondWithError(w, http.StatusBadRequest, "Unable to parse from file", err)
		return
	}
	defer r.MultipartForm.RemoveAll()

	// Parse video data
	mediatype, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if mediatype != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Invalid mime type", err)
		return
	}
	// fileExtension := strings.Split(mediatype, "/")[1]

	// Create the temp file
	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error writing temp file to disk", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Copy data to the temp file
	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error writing file to disk", err)
		return
	}
	
	// Reset pointer to start of Tempfile for write
	_, err = tempFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error writing file to disk", err)
		return
	}

	// Create video key
	fileKey := getAssetPath(mediatype)
	// Upload to S3
	s3Params := s3.PutObjectInput{
		Bucket: &cfg.s3Bucket,
		Key: aws.String(fileKey),
		ContentType: aws.String("video/mp4"),
		Body: tempFile,
	}
	cfg.s3Client.PutObject(r.Context(), &s3Params)

	// Update video URL in database
	videoURL := cfg.getObjectURL(fileKey)
	video.VideoURL = &videoURL
	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error updating thumbnail URL", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}

func makeRandomKey(bytes int) ([]byte, error) {
	randomBytes := make([]byte, bytes)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return []byte{}, err
	}
	return randomBytes, nil
}