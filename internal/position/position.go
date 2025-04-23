package position

import (
	"bufio"
	"bytes"
	"fmt"
	"unicode/utf8"

	"github.com/FrancescoCarrabino/grasshopper/internal/lsp" // <<< ADJUST GITHUB USERNAME
)

// PositionToOffset converts LSP Position (0-based Line, 0-based UTF-16 Character) to byte offset (0-based).
func PositionToOffset(content []byte, pos lsp.Position) (int, error) {
	if pos.Line < 0 {
		return 0, fmt.Errorf("invalid position: line %d is negative", pos.Line)
	}

	currentLine := 0
	byteOffset := 0
	scanner := bufio.NewScanner(bytes.NewReader(content))

	for scanner.Scan() {
		lineBytes := scanner.Bytes() // Bytes of the current line (excluding newline)
		lineLenBytes := len(lineBytes)

		if currentLine == pos.Line {
			// Found the target line
			if pos.Character < 0 {
				return 0, fmt.Errorf("invalid position: character %d is negative", pos.Character)
			}

			// Convert the line to a Go string ([]rune handles UTF-8 correctly)
			lineRunes := []rune(string(lineBytes))

			// Convert line runes to UTF-16 code units count
			// Note: A simple len(utf16.Encode(lineRunes)) is tempting but inefficient
			utf16CodeUnitCount := 0
			for _, r := range lineRunes {
				if r > 0xFFFF {
					utf16CodeUnitCount += 2 // Surrogate pair
				} else {
					utf16CodeUnitCount += 1
				}
			}

			if pos.Character > utf16CodeUnitCount {
				// Position character is beyond the actual UTF-16 length of the line
				// Clamp to the end of the line (byte offset)
				// log.Printf("Warning: Position character %d > line length %d (UTF-16 units). Clamping to line end bytes.", pos.Character, utf16CodeUnitCount)
				return byteOffset + lineLenBytes, nil
			}

			// Iterate through runes to find the byte offset corresponding to the UTF-16 character offset
			utf16CharCount := 0
			runeOffsetBytes := 0
			for _, r := range lineRunes {
				if utf16CharCount >= pos.Character {
					break // We have reached or passed the target UTF-16 character count
				}

				runeSize := utf8.RuneLen(r) // Get byte size of this rune

				// Check if the rune takes one or two UTF-16 code units
				utf16Size := 1
				if r > 0xFFFF {
					utf16Size = 2
				}

				// Check if adding this rune *would exceed* the target character count
				if utf16CharCount+utf16Size > pos.Character {
					// This rune crosses the boundary. The offset should be *before* this rune.
					break
				}

				// Otherwise, add this rune's contribution and advance
				utf16CharCount += utf16Size
				runeOffsetBytes += runeSize // Advance byte offset
			}
			// runeOffsetBytes now holds the byte offset within the line
			return byteOffset + runeOffsetBytes, nil
		}

		// Move to the next line. Add bytes of current line + 1 byte for the assumed newline.
		// This assumes Linux/Unix line endings (\n). Might need adjustment for Windows (\r\n).
		// bufio.Scanner handles stripping \r\n or \n correctly. We just need to account
		// for the delimiter size when calculating offset for the *next* line.
		// The +1 assumes single-byte newline (\n).
		// A more robust way might examine scanner.Bytes() for \r\n, but +1 is usually sufficient.
		byteOffset += lineLenBytes + 1
		currentLine++
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("error scanning content: %w", err)
	}

	// If position line is beyond the last line, return the end offset
	if currentLine <= pos.Line {
		return len(content), nil
	}

	// Should be unreachable if logic is sound
	return 0, fmt.Errorf("logic error: failed to find offset for line %d", pos.Line)
}
