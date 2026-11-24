package textutil

import (
	"regexp"
	"strconv"
	"strings"
)

var ones = []string{"không", "một", "hai", "ba", "bốn", "năm", "sáu", "bảy", "tám", "chín"}

// NumberToVietnameseWords converts int64 to Vietnamese words.
func NumberToVietnameseWords(n int64) string {
	if n == 0 {
		return "không"
	}
	if n < 0 {
		return "âm " + NumberToVietnameseWords(-n)
	}
	return readThreeDigitsGroup(n, false)
}

func readThreeDigitsGroup(n int64, hasHigherGroup bool) string {
	if n == 0 {
		return ""
	}

	// Billions (>= 1,000,000,000)
	if n >= 1000000000 {
		ty := n / 1000000000
		rem := n % 1000000000
		res := readThreeDigitsGroup(ty, hasHigherGroup) + " tỷ"
		if rem > 0 {
			res += " " + readThreeDigitsGroup(rem, true)
		}
		return res
	}

	// Millions (>= 1,000,000)
	if n >= 1000000 {
		trieu := n / 1000000
		rem := n % 1000000
		res := readThreeDigitsGroup(trieu, hasHigherGroup) + " triệu"
		if rem > 0 {
			res += " " + readThreeDigitsGroup(rem, true)
		}
		return res
	}

	// Thousands (>= 1,000)
	if n >= 1000 {
		nghin := n / 1000
		rem := n % 1000
		res := readThreeDigitsGroup(nghin, hasHigherGroup) + " nghìn"
		if rem > 0 {
			res += " " + readThreeDigitsGroup(rem, true)
		}
		return res
	}

	// Hundreds, Tens, Units (< 1000)
	hundred := n / 100
	rem := n % 100

	var parts []string

	if hundred > 0 || hasHigherGroup {
		parts = append(parts, ones[hundred]+" trăm")
	}

	if rem > 0 {
		ten := rem / 10
		unit := rem % 10

		if ten > 1 {
			parts = append(parts, ones[ten]+" mươi")
			if unit == 1 {
				parts = append(parts, "mốt")
			} else if unit == 5 {
				parts = append(parts, "lăm")
			} else if unit > 0 {
				parts = append(parts, ones[unit])
			}
		} else if ten == 1 {
			parts = append(parts, "mười")
			if unit == 5 {
				parts = append(parts, "lăm")
			} else if unit > 0 {
				parts = append(parts, ones[unit])
			}
		} else { // ten == 0
			if hundred > 0 || hasHigherGroup {
				parts = append(parts, "lẻ")
			}
			parts = append(parts, ones[unit])
		}
	}

	return strings.Join(parts, " ")
}

// DigitsToVietnameseWords reads each digit individually (e.g. for code/episode 01).
func DigitsToVietnameseWords(digits string) string {
	var words []string
	for _, ch := range digits {
		if ch >= '0' && ch <= '9' {
			words = append(words, ones[ch-'0'])
		}
	}
	return strings.Join(words, " ")
}

var (
	reFormattedNum = regexp.MustCompile(`\b(\d{1,3})[.,](\d{3})\b`)
	reAlphanumeric = regexp.MustCompile(`(?i)\b([a-z]+)(\d+)\b|\b(\d+)([a-z]+)\b`)
	reDigit        = regexp.MustCompile(`\d+`)
)

// NormalizeVietnameseTextReplaceNumbers scans and replaces numbers intelligently with Vietnamese words.
func NormalizeVietnameseTextReplaceNumbers(text string) string {
	// Remove thousands separators
	text = reFormattedNum.ReplaceAllString(text, "$1$2")

	// 1. Alphanumeric (H10, S01E02, 1080p)
	text = reAlphanumeric.ReplaceAllStringFunc(text, func(match string) string {
		return reDigit.ReplaceAllStringFunc(match, func(d string) string {
			return " " + DigitsToVietnameseWords(d) + " "
		})
	})

	// 2. Convert remaining numbers
	return reDigit.ReplaceAllStringFunc(text, func(match string) string {
		// Leading zeros (01, 007) -> Read digit by digit
		if len(match) > 1 && strings.HasPrefix(match, "0") {
			return DigitsToVietnameseWords(match)
		}
		// Too long number (> 10 digits) -> Read digit by digit
		if len(match) > 10 {
			return DigitsToVietnameseWords(match)
		}

		num, err := strconv.ParseInt(match, 10, 64)
		if err != nil {
			return DigitsToVietnameseWords(match)
		}
		return NumberToVietnameseWords(num)
	})
}
