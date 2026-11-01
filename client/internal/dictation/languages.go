package dictation

import (
	"strings"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
)

// spokenLanguage is one Whisper language code with its native name. The codes
// mirror the Server's WHISPER_LANGUAGES set; the names stay in their own
// language so the list is readable without translating every entry.
type spokenLanguage struct {
	code string
	name string
}

// spokenLanguages lists the codes POST /transcribe accepts. Keep this in step
// with server/kitsune_server/languages.py when a Server gains a language.
var spokenLanguages = []spokenLanguage{
	{"af", "Afrikaans"},
	{"am", "አማርኛ"},
	{"ar", "العربية"},
	{"as", "অসমীয়া"},
	{"az", "Azərbaycan"},
	{"ba", "Башҡортса"},
	{"be", "Беларуская"},
	{"bg", "Български"},
	{"bn", "বাংলা"},
	{"bo", "བོད་ཡིག"},
	{"br", "Brezhoneg"},
	{"bs", "Bosanski"},
	{"ca", "Català"},
	{"cs", "Čeština"},
	{"cy", "Cymraeg"},
	{"da", "Dansk"},
	{"de", "Deutsch"},
	{"el", "Ελληνικά"},
	{"en", "English"},
	{"es", "Español"},
	{"et", "Eesti"},
	{"eu", "Euskara"},
	{"fa", "فارسی"},
	{"fi", "Suomi"},
	{"fo", "Føroyskt"},
	{"fr", "Français"},
	{"gl", "Galego"},
	{"gu", "ગુજરાતી"},
	{"ha", "Hausa"},
	{"haw", "ʻŌlelo Hawaiʻi"},
	{"he", "עברית"},
	{"hi", "हिन्दी"},
	{"hr", "Hrvatski"},
	{"ht", "Kreyòl ayisyen"},
	{"hu", "Magyar"},
	{"hy", "Հայերեն"},
	{"id", "Bahasa Indonesia"},
	{"is", "Íslenska"},
	{"it", "Italiano"},
	{"ja", "日本語"},
	{"jw", "Basa Jawa"},
	{"ka", "ქართული"},
	{"kk", "Қазақша"},
	{"km", "ខ្មែរ"},
	{"kn", "ಕನ್ನಡ"},
	{"ko", "한국어"},
	{"la", "Latina"},
	{"lb", "Lëtzebuergesch"},
	{"ln", "Lingála"},
	{"lo", "ລາວ"},
	{"lt", "Lietuvių"},
	{"lv", "Latviešu"},
	{"mg", "Malagasy"},
	{"mi", "Māori"},
	{"mk", "Македонски"},
	{"ml", "മലയാളം"},
	{"mn", "Монгол"},
	{"mr", "मराठी"},
	{"ms", "Bahasa Melayu"},
	{"mt", "Malti"},
	{"my", "မြန်မာ"},
	{"ne", "नेपाली"},
	{"nl", "Nederlands"},
	{"nn", "Nynorsk"},
	{"no", "Norsk"},
	{"oc", "Occitan"},
	{"pa", "ਪੰਜਾਬੀ"},
	{"pl", "Polski"},
	{"ps", "پښتو"},
	{"pt", "Português"},
	{"ro", "Română"},
	{"ru", "Русский"},
	{"sa", "संस्कृतम्"},
	{"sd", "سنڌي"},
	{"si", "සිංහල"},
	{"sk", "Slovenčina"},
	{"sl", "Slovenščina"},
	{"sn", "ChiShona"},
	{"so", "Soomaali"},
	{"sq", "Shqip"},
	{"sr", "Српски"},
	{"su", "Basa Sunda"},
	{"sv", "Svenska"},
	{"sw", "Kiswahili"},
	{"ta", "தமிழ்"},
	{"te", "తెలుగు"},
	{"tg", "Тоҷикӣ"},
	{"th", "ไทย"},
	{"tk", "Türkmençe"},
	{"tl", "Tagalog"},
	{"tr", "Türkçe"},
	{"tt", "Татарча"},
	{"uk", "Українська"},
	{"ur", "اردو"},
	{"uz", "Oʻzbekcha"},
	{"vi", "Tiếng Việt"},
	{"yi", "ייִדיש"},
	{"yo", "Yorùbá"},
	{"yue", "粵語"},
	{"zh", "中文"},
}

// LanguageOptions lists Automatic first, then every language the Server
// accepts. A configured code outside the list keeps an option of its own so
// saving another setting never silently changes the spoken language.
func LanguageOptions(catalog i18n.Catalog, configured string) []Option {
	options := []Option{{Value: "auto", Label: catalog.T("language.auto")}}
	configured = strings.TrimSpace(configured)
	present := configured == "" || configured == "auto"
	for _, language := range spokenLanguages {
		options = append(options, Option{Value: language.code, Label: language.name})
		if language.code == configured {
			present = true
		}
	}
	if !present {
		options = append(options, Option{Value: configured, Label: configured})
	}
	return options
}
