// PopulationSpec — configuración demográfica de la población simulada.
//
// El usuario define cómo debe ser la sociedad que se simula (género, edad,
// educación, estrato socioeconómico, etnia, religión, personas/empresas y
// región). Se inyecta en los prompts de generación de perfiles para que el
// mundo simulado siga esa composición, y queda guardada en state.json.
package simx

import (
	"strings"

	"mirofish/internal/locale"
)

// PopulationSpec replica la configuración de población del frontend.
type PopulationSpec struct {
	GenderMix     string `json:"gender_mix,omitempty"`     // mixed|men|women|nonbinary
	AgeRange      string `json:"age_range,omitempty"`      // mixed|18-25|25-40|40-60|60+
	Education     string `json:"education,omitempty"`      // mixed|basic|professional|highly_educated
	Socioeconomic string `json:"socioeconomic,omitempty"`  // mixed|low|middle|high
	Ethnicity     string `json:"ethnicity,omitempty"`      // mixed|white|indigenous|black|other
	Religion      string `json:"religion,omitempty"`       // mixed|catholic|evangelical|other|none
	OrgMix        string `json:"organization_mix,omitempty"` // people|companies|mixed
	Geographic    string `json:"geographic,omitempty"`    // texto libre (estados/ciudades)
}

// IsZero informa si no se configuró nada.
func (p *PopulationSpec) IsZero() bool {
	return p == nil || (p.GenderMix == "" && p.AgeRange == "" && p.Education == "" &&
		p.Socioeconomic == "" && p.Ethnicity == "" && p.Religion == "" &&
		p.OrgMix == "" && p.Geographic == "")
}

// Instruction devuelve el bloque de instrucciones para los prompts del LLM,
// en el idioma indicado (vacío si no hay configuración).
func (p *PopulationSpec) Instruction(loc string) string {
	if p.IsZero() {
		return ""
	}
	if loc == "" {
		loc = "es"
	}
	lines := []string{}
	add := func(label, value string) {
		if value != "" {
			lines = append(lines, "- "+label+": "+value)
		}
	}
	add(locale.T(loc, "population.genderMix", nil), locale.T(loc, "population.opt."+p.GenderMix, nil))
	add(locale.T(loc, "population.ageRange", nil), locale.T(loc, "population.opt."+p.AgeRange, nil))
	add(locale.T(loc, "population.education", nil), locale.T(loc, "population.opt."+p.Education, nil))
	add(locale.T(loc, "population.socioeconomic", nil), locale.T(loc, "population.opt."+p.Socioeconomic, nil))
	add(locale.T(loc, "population.ethnicity", nil), locale.T(loc, "population.opt."+p.Ethnicity, nil))
	add(locale.T(loc, "population.religion", nil), locale.T(loc, "population.opt."+p.Religion, nil))
	add(locale.T(loc, "population.orgMix", nil), locale.T(loc, "population.opt."+p.OrgMix, nil))
	add(locale.T(loc, "population.geographic", nil), p.Geographic)
	if len(lines) == 0 {
		return ""
	}
	return locale.T(loc, "population.instruction", nil) + "\n" + joinLines(lines)
}

// boolField interpreta valores booleanos de mapas JSON (bool, string, número).
func boolField(v any, def bool) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true" || x == "1" || strings.EqualFold(x, "yes")
	case float64:
		return x != 0
	case int:
		return x != 0
	default:
		return def
	}
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}
