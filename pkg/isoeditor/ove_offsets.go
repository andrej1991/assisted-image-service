package isoeditor

import (
	"encoding/json"
	"io"
	"os"
	"regexp"

	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
)

func ExtractOVEOffsetsFromDisk(d *disk.Disk) (*OVEOffsets, error) {
	fs, err := GetISO9660FileSystem(d)
	if err != nil {
		return nil, err
	}

	offsets := &OVEOffsets{
		Kargs: make(map[string]OffsetLength),
	}

	// Ignition offset
	ignFile, err := fs.OpenFile("/images/ignition.img", os.O_RDONLY)
	if err == nil {
		isoFile := ignFile.(*iso9660.File)
		ignOffset := int64(isoFile.Location() * 2048)
		ignLen := isoFile.Size()
		
		infoFile, err := fs.OpenFile("/coreos/igninfo.json", os.O_RDONLY)
		if err == nil {
			infoBytes, err := io.ReadAll(infoFile)
			if err == nil {
				var info ignitionInfo
				if json.Unmarshal(infoBytes, &info) == nil {
					if info.Length != 0 || info.Offset != 0 {
						ignOffset += info.Offset
						ignLen = info.Length
					}
				}
			}
		}
		offsets.IgnitionOffset = ignOffset
		offsets.IgnitionLength = ignLen
	}

	// Kargs offsets
	kargsFiles := []string{"/EFI/redhat/grub.cfg", "/isolinux/isolinux.cfg"}
	kargsConfigFile, err := fs.OpenFile("/coreos/kargs.json", os.O_RDONLY)
	if err == nil {
		kargsData, err := io.ReadAll(kargsConfigFile)
		if err == nil {
			var kargsConfig kargsConfig
			if json.Unmarshal(kargsData, &kargsConfig) == nil {
				kargsFiles = nil
				for _, f := range kargsConfig.Files {
					if f.Path != nil {
						kargsFiles = append(kargsFiles, *f.Path)
					}
				}
			}
		}
	}

	for _, kf := range kargsFiles {
		f, err := fs.OpenFile(kf, os.O_RDONLY)
		if err != nil {
			continue
		}
		isoFile := f.(*iso9660.File)
		kargsOff := int64(isoFile.Location() * 2048)
		
		content, err := io.ReadAll(f)
		if err != nil {
			continue
		}
		
		re := regexp.MustCompile(`(\n#*)# COREOS_KARG_EMBED_AREA`)
		submatchIndexes := re.FindSubmatchIndex(content)
		if len(submatchIndexes) == 4 {
			finalOff := kargsOff + int64(submatchIndexes[2])
			finalLen := int64(submatchIndexes[3] - submatchIndexes[2])
			offsets.Kargs[kf] = OffsetLength{Offset: finalOff, Length: finalLen}
		}
	}

	return offsets, nil
}
