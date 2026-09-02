package handlers

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/openshift/assisted-image-service/pkg/imagestore"
	"github.com/openshift/assisted-image-service/pkg/isoeditor"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"
)

type isoHandler struct {
	ImageStore          imagestore.ImageStore
	GenerateImageStream isoeditor.StreamGeneratorFunc
	client              *AssistedServiceClient
	// second arg is an HTTP response code to use when the error != nil
	urlParser func(*http.Request) (*imageDownloadParams, int, error)
	oveSem    *semaphore.Weighted
}

var _ http.Handler = &isoHandler{}

type imageDownloadParams struct {
	imageID   string
	version   string
	imageType string
	arch      string
}

func (h *isoHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	params, statusCode, err := h.urlParser(r)

	if err != nil {
		w.WriteHeader(statusCode)
		_, err = w.Write([]byte(err.Error()))
		if err != nil {
			log.Errorf("Failed to write response: %v\n", err)
		}
		return
	}

	if !h.ImageStore.HaveVersion(params.version, params.arch) {
		log.Errorf("version for %s %s, not found", params.version, params.arch)
		http.NotFound(w, r)
		return
	}

	ignition, lastModified, statusCode, err := h.client.ignitionContent(r, params.imageID, params.imageType)
	if err != nil {
		log.Errorf("Error retrieving ignition content: %v\n", err)
		w.WriteHeader(statusCode)
		return
	}

	var ramdisk []byte
	if params.imageType == imagestore.ImageTypeMinimal {
		ramdisk, statusCode, err = h.client.ramdiskContent(r, params.imageID)
		if err != nil {
			log.Errorf("Error retrieving ramdisk content: %v\n", err)
			w.WriteHeader(statusCode)
			return
		}
	}

	var kargs []byte
	kargs, statusCode, err = h.client.discoveryKernelArguments(r, params.imageID)
	if err != nil {
		log.Errorf("Error retrieving kernel arguments content: %v\n", err)
		w.WriteHeader(statusCode)
		return
	}

	if kargs != nil && params.arch == "s390x" {
		httpErrorf(w, http.StatusBadRequest, "kargs cannot be modified in s390x architecture ISOs")
		return
	}

	var baseReader io.ReadSeekCloser
	var offsets *isoeditor.OVEOffsets
	isoPath := h.ImageStore.PathForParams(params.imageType, params.version, params.arch)

	if params.imageType == imagestore.ImageTypeDisconnectedIso {
		url := h.ImageStore.URLForParams(params.imageType, params.version, params.arch)
		if url != "" && offsets != nil || true {
			offsets = h.ImageStore.GetOVEOffsets(params.version, params.arch)
			if offsets != nil {
				if err := h.oveSem.Acquire(r.Context(), 1); err != nil {
					log.Errorf("Failed to acquire OVE semaphore: %v", err)
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
			defer h.oveSem.Release(1)

			baseReader, err = h.ImageStore.CreateHTTPReader(url)
			if err != nil {
				log.Errorf("Failed to create HTTPReader for remote OVE image: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			defer baseReader.Close()
			}
		}
	}

	isoReader, err := h.GenerateImageStream(isoPath, baseReader, ignition, ramdisk, kargs, offsets)
	if err != nil {
		log.Errorf("Error creating image stream: %v\n", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer isoReader.Close()

	fileName := fmt.Sprintf("%s-discovery.iso", params.imageID)
	if params.imageType == imagestore.ImageTypeDisconnectedIso {
		fileName = fmt.Sprintf("agent-ove.%s.iso", params.arch)
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fileName))
	modTime, err := http.ParseTime(lastModified)
	if err != nil {
		log.Warnf("Error parsing last modified time %s: %v", lastModified, err)
		modTime = time.Now()
	}
	http.ServeContent(w, r, fileName, modTime, isoReader)
}
