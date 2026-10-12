package httpapi

import (
	"encoding/json"
	"sort"
	"unicode/utf16"
)

type ragCandidate struct {
	item         map[string]any
	semanticRank int
	lexicalScore float64
	lexicalMatch bool
}

// Rank the deduplicated lexical union before fusing it with semantic ranks.
func ragFuse(candidates []ragCandidate) []map[string]any {
	byID := map[string]*ragCandidate{}
	for _, candidate := range candidates {
		id := stringValue(candidate.item, "id")
		if prior := byID[id]; prior != nil {
			if candidate.lexicalMatch && (!prior.lexicalMatch || candidate.lexicalScore > prior.lexicalScore) {
				prior.lexicalMatch, prior.lexicalScore = true, candidate.lexicalScore
			}
			continue
		}
		value := candidate
		byID[id] = &value
	}
	lexical := []*ragCandidate{}
	for _, candidate := range byID {
		candidate.item["rankScore"] = float64(0)
		if candidate.semanticRank > 0 {
			candidate.item["rankScore"] = 1.0 / float64(60+candidate.semanticRank)
		}
		if candidate.lexicalMatch {
			lexical = append(lexical, candidate)
		}
	}
	sort.Slice(lexical, func(i, j int) bool {
		if lexical[i].lexicalScore != lexical[j].lexicalScore {
			return lexical[i].lexicalScore > lexical[j].lexicalScore
		}
		return stringValue(lexical[i].item, "id") < stringValue(lexical[j].item, "id")
	})
	for i, candidate := range lexical[:min(100, len(lexical))] {
		candidate.item["rankScore"] = candidate.item["rankScore"].(float64) + 1.0/float64(61+i)
	}
	results := []map[string]any{}
	for _, candidate := range byID {
		if candidate.item["rankScore"].(float64) > 0 {
			results = append(results, candidate.item)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i]["rankScore"] != results[j]["rankScore"] {
			return results[i]["rankScore"].(float64) > results[j]["rankScore"].(float64)
		}
		return stringValue(results[i], "id") < stringValue(results[j], "id")
	})
	// Keep the bounded query union intact until response bounding, which counts
	// every deduplicated candidate excluded by the caller's limit or byte budget.
	return results
}

func ragBoundEvidence(items []map[string]any, semantic map[string]any, limit, budget int) (map[string]any, error) {
	results := []map[string]any{}
	omitted := 0
	for _, item := range items {
		if len(results) >= limit {
			omitted++
			continue
		}
		excerpt := stringValue(item, "text")
		location := item["sourceLocation"].(map[string]any)
		start := location["start"].(int)
		citations := item["relationships"].([]map[string]any)
		originalCount := len(citations)
		for {
			candidate := append(append([]map[string]any{}, results...), item)
			encoded, err := json.Marshal(map[string]any{"results": candidate, "resultsOmitted": omitted, "semantic": semantic})
			if err != nil {
				return nil, err
			}
			if len(encoded)+1 <= budget-32 {
				results = candidate
				break
			}
			if len(results) > 0 {
				omitted++
				break
			}
			if len(citations) > 0 {
				citations = citations[:len(citations)-1]
				item["relationships"], item["relationshipsOmitted"] = citations, originalCount-len(citations)
				continue
			}
			length := len(utf16.Encode([]rune(excerpt)))
			if length <= 32 {
				omitted++
				break
			}
			excerpt = ragExcerpt(excerpt, max(32, length/2))
			item["text"], item["textTruncated"] = excerpt, true
			if _, lexical := item["queryMatchScope"]; lexical {
				// Bounding may remove a matching token. Retain record-level recall
				// without claiming every lexical term remains in the excerpt.
				item["queryMatchScope"] = "record"
			}
			location["end"] = start + len(utf16.Encode([]rune(excerpt)))
		}
	}
	return map[string]any{"results": results, "resultsOmitted": omitted, "semantic": semantic}, nil
}
