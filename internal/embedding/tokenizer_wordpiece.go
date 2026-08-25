package embedding

type wordPieceTokenizer struct {
	vocab            map[string]int
	unknownID        int
	maxInputChars    int
	continuingPrefix string
}

func (t *wordPieceTokenizer) encode(word string) []int {
	runes := []rune(word)
	if len(runes) == 0 || len(runes) > t.maxInputChars {
		return []int{t.unknownID}
	}
	pieces := make([]int, 0, len(runes))
	for start := 0; start < len(runes); {
		end, found := len(runes), false
		for start < end {
			piece := string(runes[start:end])
			if start > 0 {
				piece = t.continuingPrefix + piece
			}
			if id, ok := t.vocab[piece]; ok {
				pieces = append(pieces, id)
				start = end
				found = true
				break
			}
			end--
		}
		if !found {
			return []int{t.unknownID}
		}
	}
	return pieces
}
