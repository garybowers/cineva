package media

import (
	"regexp"
	"strings"
	"unicode"
)

// creditsTitles are the chapter names that mark a film's end credits, as
// creditsName leaves them: lower case, without a chapter number in front
// or punctuation round the edges. A name has to be one of these outright —
// "Credits" is the credits, "Credits and post-credits scene" is not
// something to skip.
var creditsTitles = map[string]bool{
	// English
	"credits": true, "end credits": true, "end credit": true, "closing credits": true,
	"final credits": true, "end titles": true, "end title": true, "closing titles": true,
	"credits roll": true, "end crawl": true,
	// German
	"abspann": true, "nachspann": true,
	// French
	"générique de fin": true, "generique de fin": true, "générique final": true,
	"generique final": true, "générique": true, "generique": true,
	// Spanish and Portuguese
	"créditos": true, "creditos": true, "créditos finales": true, "creditos finales": true,
	"créditos finais": true, "creditos finais": true,
	// Italian
	"titoli di coda": true, "crediti": true, "crediti finali": true,
	// Dutch
	"aftiteling": true, "eindgeneriek": true,
	// Scandinavian
	"eftertexter": true, "sluttexter": true, "rulletekster": true, "rulletekst": true,
	"slutrulletekster": true, "sluttekst": true, "lopputekstit": true,
	// Central and eastern European
	"napisy końcowe": true, "napisy koncowe": true, "závěrečné titulky": true,
	"титры": true, "финальные титры": true,
	// Japanese
	"エンドクレジット": true, "スタッフロール": true,
}

// chapterNumber is a chapter number written in front of a name: "12. ",
// "Chapter 12 - ", "Kapitel 3: ".
var chapterNumber = regexp.MustCompile(`^(?:chapter|chapitre|kapitel|capítulo|capitulo|capitolo|hoofdstuk|kapittel)?\s*\d+\s*[-.:)–]?\s*`)

// creditsName reduces a chapter's name to the form creditsTitles holds.
func creditsName(title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	t = chapterNumber.ReplaceAllString(t, "")
	t = strings.TrimFunc(t, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
	return strings.Join(strings.Fields(t), " ")
}

// Bounds on what a credits chapter may be. Shorter is a logo or a sting;
// longer than half an hour is a mislabelled chapter, or one chapter for
// the whole second half. It must start in the second half of the film, so
// a disc whose first chapter is "Opening credits" — or a stray "Credits"
// somewhere in the middle — is never taken for the end.
const (
	minCreditsSecs = 15
	maxCreditsSecs = 30 * 60
)

// CreditsChapter finds a film's end credits among its chapter marks: the
// last chapter, or the one before it (credits followed by a post-credits
// scene), named as end credits are in any of the common languages. It
// returns where they start and end; ok is false when no chapter qualifies.
// runtimeSecs is the film's running time, 0 when unknown (the last chapter's
// end then stands for it).
func CreditsChapter(chapters []Chapter, runtimeSecs int) (start, end float64, ok bool) {
	if len(chapters) < 2 {
		return 0, 0, false // a single chapter is the whole film
	}
	total := float64(runtimeSecs)
	if total <= 0 {
		total = chapters[len(chapters)-1].EndSecs
	}
	for i := len(chapters) - 1; i >= max(1, len(chapters)-2); i-- {
		c := chapters[i]
		if !creditsTitles[creditsName(c.Title)] {
			continue
		}
		dur := c.EndSecs - c.StartSecs
		if dur < minCreditsSecs || dur > maxCreditsSecs || c.StartSecs < total/2 {
			return 0, 0, false
		}
		return c.StartSecs, c.EndSecs, true
	}
	return 0, 0, false
}
