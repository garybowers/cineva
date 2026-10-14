// Package lang names languages and folds the many ways files tag them into
// one code. It is a table and nothing else, so packages that only need a
// language's name (the preference registry) do not pull in the media stack.
package lang

import (
	"slices"
	"strings"
)

// language is one entry of the table: its code (ISO 639-1 where the
// language has one, else ISO 639-2), its English name, and the other
// spellings that mean it.
type language struct {
	code  string
	name  string
	other []string // ISO 639-2 bibliographic and terminological codes, retired codes
}

// languages is every ISO 639-1 language, with its ISO 639-2 codes in both
// forms where they differ ("fre" and "fra"), plus the few without a 639-1
// code that video files carry often enough to matter.
var languages = []language{
	{"aa", "Afar", []string{"aar"}},
	{"ab", "Abkhazian", []string{"abk"}},
	{"ae", "Avestan", []string{"ave"}},
	{"af", "Afrikaans", []string{"afr"}},
	{"ak", "Akan", []string{"aka"}},
	{"am", "Amharic", []string{"amh"}},
	{"an", "Aragonese", []string{"arg"}},
	{"ar", "Arabic", []string{"ara"}},
	{"as", "Assamese", []string{"asm"}},
	{"av", "Avaric", []string{"ava"}},
	{"ay", "Aymara", []string{"aym"}},
	{"az", "Azerbaijani", []string{"aze"}},
	{"ba", "Bashkir", []string{"bak"}},
	{"be", "Belarusian", []string{"bel"}},
	{"bg", "Bulgarian", []string{"bul"}},
	{"bi", "Bislama", []string{"bis"}},
	{"bm", "Bambara", []string{"bam"}},
	{"bn", "Bengali", []string{"ben"}},
	{"bo", "Tibetan", []string{"tib", "bod"}},
	{"br", "Breton", []string{"bre"}},
	{"bs", "Bosnian", []string{"bos"}},
	{"ca", "Catalan", []string{"cat"}},
	{"ce", "Chechen", []string{"che"}},
	{"ch", "Chamorro", []string{"cha"}},
	{"co", "Corsican", []string{"cos"}},
	{"cr", "Cree", []string{"cre"}},
	{"cs", "Czech", []string{"cze", "ces"}},
	{"cu", "Church Slavonic", []string{"chu"}},
	{"cv", "Chuvash", []string{"chv"}},
	{"cy", "Welsh", []string{"wel", "cym"}},
	{"da", "Danish", []string{"dan"}},
	{"de", "German", []string{"ger", "deu"}},
	{"dv", "Divehi", []string{"div"}},
	{"dz", "Dzongkha", []string{"dzo"}},
	{"ee", "Ewe", []string{"ewe"}},
	{"el", "Greek", []string{"gre", "ell"}},
	{"en", "English", []string{"eng"}},
	{"eo", "Esperanto", []string{"epo"}},
	{"es", "Spanish", []string{"spa", "castilian"}},
	{"et", "Estonian", []string{"est"}},
	{"eu", "Basque", []string{"baq", "eus"}},
	{"fa", "Persian", []string{"per", "fas", "farsi"}},
	{"ff", "Fula", []string{"ful"}},
	{"fi", "Finnish", []string{"fin"}},
	{"fj", "Fijian", []string{"fij"}},
	{"fo", "Faroese", []string{"fao"}},
	{"fr", "French", []string{"fre", "fra"}},
	{"fy", "Western Frisian", []string{"fry", "frisian"}},
	{"ga", "Irish", []string{"gle"}},
	{"gd", "Scottish Gaelic", []string{"gla", "gaelic"}},
	{"gl", "Galician", []string{"glg"}},
	{"gn", "Guarani", []string{"grn"}},
	{"gu", "Gujarati", []string{"guj"}},
	{"gv", "Manx", []string{"glv"}},
	{"ha", "Hausa", []string{"hau"}},
	{"he", "Hebrew", []string{"heb", "iw"}},
	{"hi", "Hindi", []string{"hin"}},
	{"ho", "Hiri Motu", []string{"hmo"}},
	{"hr", "Croatian", []string{"hrv", "scr"}},
	{"ht", "Haitian Creole", []string{"hat", "haitian"}},
	{"hu", "Hungarian", []string{"hun"}},
	{"hy", "Armenian", []string{"arm", "hye"}},
	{"hz", "Herero", []string{"her"}},
	{"ia", "Interlingua", []string{"ina"}},
	{"id", "Indonesian", []string{"ind", "in"}},
	{"ie", "Interlingue", []string{"ile"}},
	{"ig", "Igbo", []string{"ibo"}},
	{"ii", "Sichuan Yi", []string{"iii"}},
	{"ik", "Inupiaq", []string{"ipk"}},
	{"io", "Ido", []string{"ido"}},
	{"is", "Icelandic", []string{"ice", "isl"}},
	{"it", "Italian", []string{"ita"}},
	{"iu", "Inuktitut", []string{"iku"}},
	{"ja", "Japanese", []string{"jpn"}},
	{"jv", "Javanese", []string{"jav"}},
	{"ka", "Georgian", []string{"geo", "kat"}},
	{"kg", "Kongo", []string{"kon"}},
	{"ki", "Kikuyu", []string{"kik"}},
	{"kj", "Kuanyama", []string{"kua"}},
	{"kk", "Kazakh", []string{"kaz"}},
	{"kl", "Greenlandic", []string{"kal", "kalaallisut"}},
	{"km", "Khmer", []string{"khm"}},
	{"kn", "Kannada", []string{"kan"}},
	{"ko", "Korean", []string{"kor"}},
	{"kr", "Kanuri", []string{"kau"}},
	{"ks", "Kashmiri", []string{"kas"}},
	{"ku", "Kurdish", []string{"kur"}},
	{"kv", "Komi", []string{"kom"}},
	{"kw", "Cornish", []string{"cor"}},
	{"ky", "Kyrgyz", []string{"kir", "kirghiz"}},
	{"la", "Latin", []string{"lat"}},
	{"lb", "Luxembourgish", []string{"ltz"}},
	{"lg", "Ganda", []string{"lug"}},
	{"li", "Limburgish", []string{"lim"}},
	{"ln", "Lingala", []string{"lin"}},
	{"lo", "Lao", []string{"lao"}},
	{"lt", "Lithuanian", []string{"lit"}},
	{"lu", "Luba-Katanga", []string{"lub"}},
	{"lv", "Latvian", []string{"lav"}},
	{"mg", "Malagasy", []string{"mlg"}},
	{"mh", "Marshallese", []string{"mah"}},
	{"mi", "Maori", []string{"mao", "mri"}},
	{"mk", "Macedonian", []string{"mac", "mkd"}},
	{"ml", "Malayalam", []string{"mal"}},
	{"mn", "Mongolian", []string{"mon"}},
	{"mr", "Marathi", []string{"mar"}},
	{"ms", "Malay", []string{"may", "msa"}},
	{"mt", "Maltese", []string{"mlt"}},
	{"my", "Burmese", []string{"bur", "mya"}},
	{"na", "Nauru", []string{"nau"}},
	{"nd", "North Ndebele", []string{"nde"}},
	{"ne", "Nepali", []string{"nep"}},
	{"ng", "Ndonga", []string{"ndo"}},
	{"nl", "Dutch", []string{"dut", "nld", "flemish"}},
	// Bokmål and Nynorsk are both Norwegian to a viewer choosing a
	// language; files use all three codes.
	{"no", "Norwegian", []string{"nor", "nb", "nob", "nn", "nno", "norwegian bokmål", "norwegian nynorsk"}},
	{"nr", "South Ndebele", []string{"nbl"}},
	{"nv", "Navajo", []string{"nav"}},
	{"ny", "Chichewa", []string{"nya"}},
	{"oc", "Occitan", []string{"oci"}},
	{"oj", "Ojibwe", []string{"oji"}},
	{"om", "Oromo", []string{"orm"}},
	{"or", "Odia", []string{"ori", "oriya"}},
	{"os", "Ossetian", []string{"oss"}},
	{"pa", "Punjabi", []string{"pan", "panjabi"}},
	{"pi", "Pali", []string{"pli"}},
	{"pl", "Polish", []string{"pol"}},
	{"ps", "Pashto", []string{"pus"}},
	{"pt", "Portuguese", []string{"por"}},
	{"qu", "Quechua", []string{"que"}},
	{"rm", "Romansh", []string{"roh"}},
	{"rn", "Kirundi", []string{"run", "rundi"}},
	{"ro", "Romanian", []string{"rum", "ron", "moldavian"}},
	{"ru", "Russian", []string{"rus"}},
	{"rw", "Kinyarwanda", []string{"kin"}},
	{"sa", "Sanskrit", []string{"san"}},
	{"sc", "Sardinian", []string{"srd"}},
	{"sd", "Sindhi", []string{"snd"}},
	{"se", "Northern Sami", []string{"sme"}},
	{"sg", "Sango", []string{"sag"}},
	{"si", "Sinhala", []string{"sin", "sinhalese"}},
	{"sk", "Slovak", []string{"slo", "slk"}},
	{"sl", "Slovenian", []string{"slv", "slovene"}},
	{"sm", "Samoan", []string{"smo"}},
	{"sn", "Shona", []string{"sna"}},
	{"so", "Somali", []string{"som"}},
	{"sq", "Albanian", []string{"alb", "sqi"}},
	{"sr", "Serbian", []string{"srp", "scc"}},
	{"ss", "Swati", []string{"ssw"}},
	{"st", "Sesotho", []string{"sot"}},
	{"su", "Sundanese", []string{"sun"}},
	{"sv", "Swedish", []string{"swe"}},
	{"sw", "Swahili", []string{"swa"}},
	{"ta", "Tamil", []string{"tam"}},
	{"te", "Telugu", []string{"tel"}},
	{"tg", "Tajik", []string{"tgk"}},
	{"th", "Thai", []string{"tha"}},
	{"ti", "Tigrinya", []string{"tir"}},
	{"tk", "Turkmen", []string{"tuk"}},
	{"tl", "Tagalog", []string{"tgl"}},
	{"tn", "Tswana", []string{"tsn"}},
	{"to", "Tongan", []string{"ton"}},
	{"tr", "Turkish", []string{"tur"}},
	{"ts", "Tsonga", []string{"tso"}},
	{"tt", "Tatar", []string{"tat"}},
	{"tw", "Twi", []string{"twi"}},
	{"ty", "Tahitian", []string{"tah"}},
	{"ug", "Uyghur", []string{"uig"}},
	{"uk", "Ukrainian", []string{"ukr"}},
	{"ur", "Urdu", []string{"urd"}},
	{"uz", "Uzbek", []string{"uzb"}},
	{"ve", "Venda", []string{"ven"}},
	{"vi", "Vietnamese", []string{"vie"}},
	{"vo", "Volapük", []string{"vol"}},
	{"wa", "Walloon", []string{"wln"}},
	{"wo", "Wolof", []string{"wol"}},
	{"xh", "Xhosa", []string{"xho"}},
	{"yi", "Yiddish", []string{"yid", "ji"}},
	{"yo", "Yoruba", []string{"yor"}},
	{"za", "Zhuang", []string{"zha"}},
	// Mandarin is what "Chinese" means on nearly every file that says
	// cmn; Cantonese (below) is kept apart, as players and discs keep it.
	{"zh", "Chinese", []string{"chi", "zho", "cmn", "mandarin"}},
	{"zu", "Zulu", []string{"zul"}},

	// No ISO 639-1 code; tagged by their ISO 639-2 or 639-3 one.
	{"fil", "Filipino", nil},
	{"haw", "Hawaiian", nil},
	{"tlh", "Klingon", nil},
	{"yue", "Cantonese", nil},
}

var (
	codes = map[string]string{} // every spelling, lower-cased, to its code
	names = map[string]string{} // code to English name
)

func init() {
	for _, l := range languages {
		names[l.code] = l.name
		codes[l.code] = l.code
		codes[strings.ToLower(l.name)] = l.code
		for _, s := range l.other {
			codes[s] = l.code
		}
	}
}

// Code is the one code for a language tag however a file spells it: ISO
// 639-2 in either form ("fre", "fra"), ISO 639-1 ("fr"), a region-qualified
// tag ("fr-CA"), the English name, or any of those with stray quotes from a
// muxing script. A tag the table does not know comes back lower-cased, so
// two of them still match each other. "" for no language at all, including
// "und" (undetermined).
func Code(tag string) string {
	t := strings.ToLower(strings.Trim(strings.TrimSpace(tag), `"'`))
	if t == "" || t == "und" {
		return ""
	}
	if c, ok := codes[t]; ok {
		return c
	}
	if base, _, ok := strings.Cut(strings.ReplaceAll(t, "_", "-"), "-"); ok {
		if c, ok := codes[base]; ok {
			return c
		}
	}
	return t
}

// Name is the English name for a code Code returned, and false for one the
// table does not know (an unknown tag, or one of ISO 639-2's special codes
// such as "mul" or "zxx", which name no language a viewer would choose).
func Name(code string) (string, bool) {
	n, ok := names[code]
	return n, ok
}

// NameOf is the English name for a tag as a file spells it ("fre" is
// "French"), or "" when the tag is empty or one the table does not know.
func NameOf(tag string) string { return names[Code(tag)] }

// Language is a code and its name.
type Language struct {
	Code string
	Name string
}

// Named folds tags to codes and names them, dropping the untagged, the
// unknown and duplicates, sorted by name.
func Named(tags []string) []Language {
	seen := map[string]bool{}
	var out []Language
	for _, t := range tags {
		c := Code(t)
		n, ok := names[c]
		if !ok || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, Language{Code: c, Name: n})
	}
	slices.SortFunc(out, func(a, b Language) int { return strings.Compare(a.Name, b.Name) })
	return out
}
