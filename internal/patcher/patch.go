package patcher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// applyMemoryLimit finds the memory limit line after a limits: block and
// replaces it with newValue. Exits the limits block when indentation drops
// to the same level or lower than the limits: line itself.
func applyMemoryLimit(content []byte, newValue string) ([]byte, error) {
	lines := strings.Split(string(content), "\n")
	limitsIndent := -1
	inLimits := false
	for i, line := range lines {
		if line == "" {
			continue
		}
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))

		if trimmed == "limits:" {
			limitsIndent = indent
			inLimits = true
			continue
		}
		if inLimits {
			// Exit limits block if indentation drops to limits level or less
			if indent <= limitsIndent && trimmed != "" {
				inLimits = false
				continue
			}
			if strings.HasPrefix(trimmed, "memory:") {
				parts := strings.SplitN(line, ":", 2)
				lines[i] = parts[0] + ": " + newValue
				return []byte(strings.Join(lines, "\n")), nil
			}
		}
	}
	return nil, fmt.Errorf("applyMemoryLimit: limits: block not found or has no memory: line")
}

// applyEnvVar finds an env var by key and replaces its value.
// keyValue must be in format KEY=VALUE.
func applyEnvVar(content []byte, keyValue string) ([]byte, error) {
	eqIdx := strings.Index(keyValue, "=")
	if eqIdx == -1 {
		return nil, fmt.Errorf("applyEnvVar: keyValue %q has no '='", keyValue)
	}
	key := keyValue[:eqIdx]
	newValue := keyValue[eqIdx+1:]

	lines := strings.Split(string(content), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "- name: "+key {
			// Next line should be the value line
			if i+1 < len(lines) {
				valueLine := lines[i+1]
				valueLineTrimmed := strings.TrimSpace(valueLine)
				if strings.HasPrefix(valueLineTrimmed, "value:") {
					// Preserve indentation of the value line
					indent := valueLine[:len(valueLine)-len(strings.TrimLeft(valueLine, " \t"))]
					lines[i+1] = indent + "value: " + newValue
					return []byte(strings.Join(lines, "\n")), nil
				}
			}
			return nil, fmt.Errorf("applyEnvVar: env var key %q found but next line is not a value: line", key)
		}
	}
	return nil, fmt.Errorf("applyEnvVar: env var key %q not found", key)
}

// applyImageTag finds the image: line for the specified container and replaces
// the tag portion. If containerName is empty, falls back to patching the first
// image: line found (original behavior). If the image has no ':', the tag is
// appended.
func applyImageTag(content []byte, containerName, newTag string) ([]byte, error) {
	lines := strings.Split(string(content), "\n")

	if containerName != "" {
		inContainer := false
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "- name: "+containerName {
				inContainer = true
				continue
			}
			if inContainer {
				// Stop if we hit the next container entry
				if strings.HasPrefix(trimmed, "- name:") {
					break
				}
				if strings.Contains(line, "image:") {
					colon := strings.Index(line, "image:")
					prefix := line[:colon]
					imageValue := strings.TrimSpace(line[colon+len("image:"):])
					lastColon := strings.LastIndex(imageValue, ":")
					var newImageValue string
					if lastColon == -1 {
						newImageValue = imageValue + ":" + newTag
					} else {
						newImageValue = imageValue[:lastColon] + ":" + newTag
					}
					lines[i] = prefix + "image: " + newImageValue
					return []byte(strings.Join(lines, "\n")), nil
				}
			}
		}
		return nil, fmt.Errorf("applyImageTag: image: line not found for container %q", containerName)
	}

	// Fallback: patch first image: line (used when containerName is empty)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "image:") {
			colon := strings.Index(line, "image:")
			prefix := line[:colon]
			imageValue := strings.TrimSpace(line[colon+len("image:"):])
			lastColon := strings.LastIndex(imageValue, ":")
			var newImageValue string
			if lastColon == -1 {
				newImageValue = imageValue + ":" + newTag
			} else {
				newImageValue = imageValue[:lastColon] + ":" + newTag
			}
			lines[i] = prefix + "image: " + newImageValue
			return []byte(strings.Join(lines, "\n")), nil
		}
	}
	return nil, fmt.Errorf("applyImageTag: no image: line found")
}

// unifiedDiff generates a unified diff of oldContent vs newContent for filePath.
// It writes both contents to temp files and runs diff -u.
func unifiedDiff(oldContent, newContent []byte, filePath string) string {
	oldFile, err := os.CreateTemp("", "old-*.yaml")
	if err != nil {
		return fmt.Sprintf("(diff unavailable: %v)", err)
	}
	defer os.Remove(oldFile.Name())
	oldFile.Write(oldContent)
	oldFile.Close()

	newFile, err := os.CreateTemp("", "new-*.yaml")
	if err != nil {
		return fmt.Sprintf("(diff unavailable: %v)", err)
	}
	defer os.Remove(newFile.Name())
	newFile.Write(newContent)
	newFile.Close()

	cmd := exec.Command("diff", "-u",
		"--label", "a/"+filePath,
		"--label", "b/"+filePath,
		oldFile.Name(), newFile.Name())
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			// exit code 1 = files differ, expected
		} else {
			return fmt.Sprintf("(diff unavailable: %v)", err)
		}
	}
	return string(out)
}
