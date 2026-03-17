package patcher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// applyMemoryLimit sets the memory limit for a container in a deployment YAML.
// It handles four representations of the resources block, in priority order:
//
//  1. Existing limits.memory line → update in place
//  2. limits: block without memory: → insert memory: line into limits block
//  3. resources: block without limits: → insert limits: + memory: block
//  4. resources: {} (empty inline mapping) → expand to resources: limits: memory:
//
// When containerName is non-empty, the patch is scoped to the named container
// block. When empty, the first matching block in the file is patched.
func applyMemoryLimit(content []byte, containerName, newValue string) ([]byte, error) {
	lines := strings.Split(string(content), "\n")

	startIdx := 0
	endIdx := len(lines)

	if containerName != "" {
		// Find the container block boundaries.
		inContainer := false
		containerStart := -1
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "- name: "+containerName {
				inContainer = true
				containerStart = i
				continue
			}
			if inContainer {
				if strings.HasPrefix(trimmed, "- name:") {
					startIdx = containerStart
					endIdx = i
					inContainer = false
					break
				}
			}
		}
		if containerStart == -1 {
			return nil, fmt.Errorf("applyMemoryLimit: container %q not found", containerName)
		}
		if inContainer {
			startIdx = containerStart
			endIdx = len(lines)
		}
	}

	// Case 1 & 2: scan for a limits: block and update or insert memory:.
	limitsLine := -1
	limitsIndent := -1
	inLimits := false
	memoryFound := false
	for i := startIdx; i < endIdx; i++ {
		line := lines[i]
		if line == "" {
			continue
		}
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))

		if trimmed == "limits:" {
			limitsLine = i
			limitsIndent = indent
			inLimits = true
			continue
		}
		if inLimits {
			if indent <= limitsIndent && trimmed != "" {
				// Exited limits block without finding memory: — insert it.
				// Insert before the line that caused the exit.
				fieldIndent := strings.Repeat(" ", limitsIndent+2)
				newLine := fieldIndent + "memory: " + newValue
				result := make([]string, 0, len(lines)+1)
				result = append(result, lines[:i]...)
				result = append(result, newLine)
				result = append(result, lines[i:]...)
				return []byte(strings.Join(result, "\n")), nil
			}
			if strings.HasPrefix(trimmed, "memory:") {
				// Case 1: update existing memory: line.
				parts := strings.SplitN(line, ":", 2)
				lines[i] = parts[0] + ": " + newValue
				memoryFound = true
				return []byte(strings.Join(lines, "\n")), nil
			}
		}
	}
	if inLimits && !memoryFound {
		// limits: was the last block in the search range — append memory:.
		fieldIndent := strings.Repeat(" ", limitsIndent+2)
		newLine := fieldIndent + "memory: " + newValue
		result := make([]string, 0, len(lines)+1)
		result = append(result, lines[:endIdx]...)
		result = append(result, newLine)
		result = append(result, lines[endIdx:]...)
		return []byte(strings.Join(result, "\n")), nil
	}
	if limitsLine >= 0 {
		// Should not reach here; handled above.
		return nil, fmt.Errorf("applyMemoryLimit: limits: found but memory: insertion failed")
	}

	// Case 3: resources: block present but no limits: — scan for resources:
	// and insert a limits: + memory: block after it.
	for i := startIdx; i < endIdx; i++ {
		line := lines[i]
		if line == "" {
			continue
		}
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))

		// Case 4: resources: {} (empty inline map) — replace the entire line.
		if trimmed == "resources: {}" {
			indentStr := strings.Repeat(" ", indent)
			replacement := []string{
				indentStr + "resources:",
				indentStr + "  limits:",
				indentStr + "    memory: " + newValue,
			}
			result := make([]string, 0, len(lines)+2)
			result = append(result, lines[:i]...)
			result = append(result, replacement...)
			result = append(result, lines[i+1:]...)
			return []byte(strings.Join(result, "\n")), nil
		}

		// Case 3: resources: as a block key (multi-line).
		if trimmed == "resources:" {
			indentStr := strings.Repeat(" ", indent+2)
			insertion := []string{
				indentStr + "limits:",
				indentStr + "  memory: " + newValue,
			}
			result := make([]string, 0, len(lines)+2)
			result = append(result, lines[:i+1]...)
			result = append(result, insertion...)
			result = append(result, lines[i+1:]...)
			return []byte(strings.Join(result, "\n")), nil
		}
	}

	// Case 5: no resources block at all — find the container block and append
	// a resources: limits: memory: block at the end of its field list.
	containerStart := -1
	dashIndent := -1
	for i := startIdx; i < endIdx; i++ {
		line := lines[i]
		if line == "" {
			continue
		}
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))

		if containerStart == -1 {
			if containerName != "" {
				if trimmed == "- name: "+containerName {
					containerStart = i
					dashIndent = indent
				}
			} else {
				// No container name specified: use the first list entry found.
				if strings.HasPrefix(trimmed, "- name:") {
					containerStart = i
					dashIndent = indent
				}
			}
			continue
		}

		// We are inside the container block. It ends when indentation drops
		// back to the dash level (next container or end of containers list).
		if indent <= dashIndent && trimmed != "" {
			// Insert resources block before the line that ends this container.
			fieldIndent := strings.Repeat(" ", dashIndent+2)
			insertion := []string{
				fieldIndent + "resources:",
				fieldIndent + "  limits:",
				fieldIndent + "    memory: " + newValue,
			}
			result := make([]string, 0, len(lines)+3)
			result = append(result, lines[:i]...)
			result = append(result, insertion...)
			result = append(result, lines[i:]...)
			return []byte(strings.Join(result, "\n")), nil
		}
	}

	if containerStart >= 0 {
		// Container block extends to end of search range — append resources block.
		fieldIndent := strings.Repeat(" ", dashIndent+2)
		insertion := []string{
			fieldIndent + "resources:",
			fieldIndent + "  limits:",
			fieldIndent + "    memory: " + newValue,
		}
		result := make([]string, 0, len(lines)+3)
		result = append(result, lines[:endIdx]...)
		result = append(result, insertion...)
		result = append(result, lines[endIdx:]...)
		return []byte(strings.Join(result, "\n")), nil
	}

	return nil, fmt.Errorf("applyMemoryLimit: no container or resources block found in manifest (container %q)", containerName)
}

// applyEnvVar finds an env var by key and replaces its value.
// keyValue must be in format KEY=VALUE.
//
// Assumption: the manifest uses the standard multi-line env var format:
//
//	- name: KEY
//	  value: VAL
//
// Inline style (e.g. {name: KEY, value: VAL}) is not supported.
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
				if strings.HasPrefix(trimmed, "image:") {
					colon := strings.Index(line, "image:")
					prefix := line[:colon]
					imageValue := strings.TrimSpace(line[colon+len("image:"):])
					lastColon := strings.LastIndex(imageValue, ":")
					repo := imageValue
					if lastColon != -1 {
						repo = imageValue[:lastColon]
					}
					newImageValue := repo + ":" + newTag
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
			repo := imageValue
			if lastColon != -1 {
				repo = imageValue[:lastColon]
			}
			newImageValue := repo + ":" + newTag
			lines[i] = prefix + "image: " + newImageValue
			return []byte(strings.Join(lines, "\n")), nil
		}
	}
	return nil, fmt.Errorf("applyImageTag: no image: line found")
}

// applyKustomizationImageTag finds the images: entry with the given imageName
// and updates its newTag: value to newTag.
// If the entry has no newTag: line, one is inserted after the entry's last field.
// Returns an error if the imageName entry is not found.
func applyKustomizationImageTag(content []byte, imageName, newTag string) ([]byte, error) {
	lines := strings.Split(string(content), "\n")

	imagesIndent := -1
	inImages := false
	entryIndent := -1
	inEntry := false
	entryLastLine := -1

	for i, line := range lines {
		if line == "" {
			continue
		}
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " \t"))

		if !inImages {
			if trimmed == "images:" {
				imagesIndent = indent
				inImages = true
			}
			continue
		}

		// Exit images block when we see a non-list line at or below images: indentation.
		// List items (starting with "-") at the images: indent level are valid block members.
		if indent <= imagesIndent && !strings.HasPrefix(trimmed, "-") {
			break
		}

		if !inEntry {
			// Look for the entry start: "- name: <imageName>"
			withoutDash := strings.TrimPrefix(trimmed, "- ")
			if withoutDash == "name: "+imageName {
				entryIndent = indent
				inEntry = true
				entryLastLine = i
			}
			continue
		}

		// We are inside the matching entry.
		// The entry ends when we see another list item at the entry's indent level,
		// or a non-list line at or below entry indent level.
		if indent <= entryIndent {
			break
		}

		// Still inside the entry — track last non-empty line and check for newTag.
		entryLastLine = i

		if strings.HasPrefix(trimmed, "newTag:") {
			// Replace in place, preserving indentation
			prefix := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			lines[i] = prefix + "newTag: " + newTag
			return []byte(strings.Join(lines, "\n")), nil
		}
	}

	if !inEntry {
		return nil, fmt.Errorf("applyKustomizationImageTag: image entry %q not found", imageName)
	}

	// No newTag: line found in entry; insert one after entryLastLine.
	// Indentation: entry's "-" is at entryIndent; fields are at entryIndent+2.
	fieldIndent := strings.Repeat(" ", entryIndent+2)
	newLine := fieldIndent + "newTag: " + newTag
	result := make([]string, 0, len(lines)+1)
	result = append(result, lines[:entryLastLine+1]...)
	result = append(result, newLine)
	result = append(result, lines[entryLastLine+1:]...)
	return []byte(strings.Join(result, "\n")), nil
}

// unifiedDiff generates a unified diff of oldContent vs newContent for filePath.
// It writes both contents to temp files and runs diff -u.
func unifiedDiff(oldContent, newContent []byte, filePath string) string {
	oldFile, err := os.CreateTemp("", "old-*.yaml")
	if err != nil {
		return fmt.Sprintf("(diff unavailable: %v)", err)
	}
	defer os.Remove(oldFile.Name())
	if _, werr := oldFile.Write(oldContent); werr != nil {
		oldFile.Close()
		return "(diff unavailable: write error)"
	}
	oldFile.Close()

	newFile, err := os.CreateTemp("", "new-*.yaml")
	if err != nil {
		return fmt.Sprintf("(diff unavailable: %v)", err)
	}
	defer os.Remove(newFile.Name())
	if _, werr := newFile.Write(newContent); werr != nil {
		newFile.Close()
		return "(diff unavailable: write error)"
	}
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
