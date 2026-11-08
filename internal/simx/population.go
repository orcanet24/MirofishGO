// PopulationSpec — configuración demográfica de la población simulada.
//
// El usuario define cómo debe ser la sociedad que se simula: género (checklist),
// edad, educación, estrato, etnia (abierta), religión (categoría + ramas) y
// tendencia política (ramas). Se inyecta en los prompts de generación de
// perfiles y queda guardada en state.json.
package simx

import (
	"strings"

	"mirofish/internal/locale"
	"mirofish/internal/studytypes"
)

// PopulationSpec replica la configuración de población del frontend.
type PopulationSpec struct {
	Gender            []string `json:"gender,omitempty"`             // hombre, mujer, indefinido
	AgeRange          string   `json:"age_range,omitempty"`          // mixed|18-25|25-40|40-60|60+
	Education         string   `json:"education,omitempty"`          // mixed|basic|professional|highly_educated
	Socioeconomic     string   `json:"socioeconomic,omitempty"`      // mixed|low|middle|high
	Ethnicity         string   `json:"ethnicity,omitempty"`          // texto libre + sugerencias
	Religion          string   `json:"religion,omitempty"`           // mixed|none|cristiano|judio|musulman|otro
	ReligionBranches  []string `json:"religion_branches,omitempty"`  // catolico_romano, evangelico, mormon, testigo_jehova, judio, suni, chiita, otro
	Politics          string   `json:"politics,omitempty"`           // none|mixed|custom
	PoliticsBranches  []string `json:"politics_branches,omitempty"`  // derecha, centro, izquierda, ultra_derecha, ultra_izquierda, democrata, republicano
	OrgMix            string   `json:"organization_mix,omitempty"`   // people|companies|mixed
	Geographic        string   `json:"geographic,omitempty"`         // texto libre (estados/ciudades)

	// StudyType — tipo de estudio (político, médico...). No se persiste aquí:
	// vive en el project. Se usa para decidir qué bloques inyectar.
}

// IsZero informa si no se configuró nada.
func (p *PopulationSpec) IsZero() bool {
	if p == nil {
		return true
	}
	return len(p.Gender) == 0 && p.AgeRange == "" && p.Education == "" &&
		p.Socioeconomic == "" && p.Ethnicity == "" && p.Religion == "" &&
		len(p.ReligionBranches) == 0 && p.Politics == "" && len(p.PoliticsBranches) == 0 &&
		p.OrgMix == "" && p.Geographic == ""
}

// Instruction devuelve el bloque de instrucciones para los prompts del LLM,
// en el idioma indicado (vacío si no hay configuración o no aplica al tipo).
func (p *PopulationSpec) Instruction(loc, studyTypeID string, applyPolitics, applyReligion bool) string {
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
	if len(p.Gender) > 0 {
		names := make([]string, 0, len(p.Gender))
		for _, g := range p.Gender {
			names = append(names, locale.T(loc, "population.opt."+g, nil))
		}
		add(locale.T(loc, "population.genderChecklist", nil), strings.Join(names, ", "))
	}
	add(locale.T(loc, "population.ageRange", nil), locale.T(loc, "population.opt."+p.AgeRange, nil))
	add(locale.T(loc, "population.education", nil), locale.T(loc, "population.opt."+p.Education, nil))
	add(locale.T(loc, "population.socioeconomic", nil), locale.T(loc, "population.opt."+p.Socioeconomic, nil))
	if p.Ethnicity != "" {
		add(locale.T(loc, "population.ethnicityHint", nil), p.Ethnicity)
	}
	if applyReligion && (p.Religion != "" || len(p.ReligionBranches) > 0) {
		cat := locale.T(loc, "population.opt."+p.Religion, nil)
		if len(p.ReligionBranches) > 0 {
			names := make([]string, 0, len(p.ReligionBranches))
			for _, b := range p.ReligionBranches {
				names = append(names, locale.T(loc, "population.opt."+b, nil))
			}
			cat += " (" + strings.Join(names, ", ") + ")"
		}
		add(locale.T(loc, "population.religionCategories", nil), cat)
		if note := locale.T(loc, "population.religionNote", nil); note != "" && p.hasBranch("testigo_jehova") {
			lines = append(lines, "- "+note)
		}
	}
	if applyPolitics && (p.Politics != "" || len(p.PoliticsBranches) > 0) {
		pol := locale.T(loc, "population.politicsNone", nil)
		if len(p.PoliticsBranches) > 0 {
			names := make([]string, 0, len(p.PoliticsBranches))
			for _, b := range p.PoliticsBranches {
				names = append(names, locale.T(loc, "population.opt."+b, nil))
			}
			pol = strings.Join(names, ", ")
		}
		add(locale.T(loc, "population.politics", nil), pol)
		if note := locale.T(loc, "population.politicsNote", nil); note != "" {
			lines = append(lines, "- "+note)
		}
	}
	add(locale.T(loc, "population.orgMix", nil), locale.T(loc, "population.opt."+p.OrgMix, nil))
	add(locale.T(loc, "population.geographic", nil), p.Geographic)
	if len(lines) == 0 {
		return ""
	}
	return locale.T(loc, "population.instruction", nil) + "\n" + joinLines(lines)
}

func (p *PopulationSpec) hasBranch(b string) bool {
	for _, x := range p.ReligionBranches {
		if x == b {
			return true
		}
	}
	return false
}

// GenderFromLegacy convierte gender_mix (string antiguo) a checklist.
func (p *PopulationSpec) GenderFromLegacy() {
	if p == nil {
		return
	}
	switch p.OrgMix {
	case "legacy-gender-men":
		p.Gender = []string{"hombre"}
	case "legacy-gender-women":
		p.Gender = []string{"mujer"}
	case "legacy-gender-nonbinary":
		p.Gender = []string{"indefinido"}
	case "legacy-gender-mixed":
		p.Gender = []string{"hombre", "mujer", "indefinido"}
	}
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

// parsePopulation construye el spec desde un map (state.json). Soporta el
// gender_mix antiguo (string) y el nuevo gender (checklist).
func parsePopulation(raw map[string]any) *PopulationSpec {
	p := &PopulationSpec{
		AgeRange:      strOr(raw["age_range"]),
		Education:     strOr(raw["education"]),
		Socioeconomic: strOr(raw["socioeconomic"]),
		Ethnicity:     strOr(raw["ethnicity"]),
		Religion:      strOr(raw["religion"]),
		OrgMix:        strOr(raw["organization_mix"]),
		Geographic:    strOr(raw["geographic"]),
		Politics:      strOr(raw["politics"]),
	}
	if g, ok := raw["gender"].([]any); ok {
		for _, v := range g {
			p.Gender = append(p.Gender, strOr(v))
		}
	}
	if len(p.Gender) == 0 {
		p.Gender = legacyGender(strOr(raw["gender_mix"]))
	}
	p.ReligionBranches = strList(raw["religion_branches"])
	p.PoliticsBranches = strList(raw["politics_branches"])
	return p
}

func legacyGender(s string) []string {
	switch s {
	case "men":
		return []string{"hombre"}
	case "women":
		return []string{"mujer"}
	case "nonbinary":
		return []string{"indefinido"}
	case "mixed":
		return []string{"hombre", "mujer", "indefinido"}
	default:
		return nil
	}
}

func strList(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := []string{}
	for _, x := range arr {
		if s := strOr(x); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// studyTypesFor devuelve el tipo de estudio si existe (para decidir bloques).
func studyTypesFor(id string) (studytypes.Type, bool) {
	if id == "" || !studytypes.Exists(id) {
		return studytypes.Type{}, false
	}
	return studytypes.Get(id), true
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
