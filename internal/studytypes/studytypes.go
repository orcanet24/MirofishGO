// Package studytypes — catálogo de tipos de estudio.
//
// Cada tipo de estudio define QUÉ dimensiones de perfil son relevantes para
// los agentes simulados (política, religión, etc.) y da una pista al generador
// de perfiles. El usuario elige el tipo antes de insertar la "semilla"; la UI
// despliega solo los ajustes que aplican.
package studytypes

// Type — un tipo de estudio.
type Type struct {
	ID          string
	NameKey     string // locale: studytype.<id>.name
	DescKey     string // locale: studytype.<id>.desc
	Politics    bool   // muestra perfiles de tendencia política
	Religion    bool   // muestra ramas religiosas
	Education   bool   // nivel educativo relevante
	Socioeconomic bool  // estrato relevante
	StudyType   string // (alias interno)
}

// Catalogue — tipos de estudio disponibles (orden de la UI).
var Catalogue = []Type{
	{ID: "general", NameKey: "studytype.general.name", DescKey: "studytype.general.desc"},
	{ID: "political", NameKey: "studytype.political.name", DescKey: "studytype.political.desc", Politics: true, Religion: true, Education: true, Socioeconomic: true},
	{ID: "medical", NameKey: "studytype.medical.name", DescKey: "studytype.medical.desc", Religion: true, Education: true, Socioeconomic: true},
	{ID: "brand", NameKey: "studytype.brand.name", DescKey: "studytype.brand.desc", Education: true, Socioeconomic: true},
	{ID: "product", NameKey: "studytype.product.name", DescKey: "studytype.product.desc", Education: true, Socioeconomic: true},
	{ID: "entrepreneurship", NameKey: "studytype.entrepreneurship.name", DescKey: "studytype.entrepreneurship.desc", Education: true, Socioeconomic: true},
	{ID: "marketing", NameKey: "studytype.marketing.name", DescKey: "studytype.marketing.desc", Education: true, Socioeconomic: true},
	{ID: "education", NameKey: "studytype.education.name", DescKey: "studytype.education.desc", Education: true, Socioeconomic: true, Religion: true},
	{ID: "environment", NameKey: "studytype.environment.name", DescKey: "studytype.environment.desc", Education: true, Socioeconomic: true},
	{ID: "legal", NameKey: "studytype.legal.name", DescKey: "studytype.legal.desc", Education: true, Socioeconomic: true, Politics: true},
	{ID: "tech", NameKey: "studytype.tech.name", DescKey: "studytype.tech.desc", Education: true},
	{ID: "finance", NameKey: "studytype.finance.name", DescKey: "studytype.finance.desc", Education: true, Socioeconomic: true},
	{ID: "social", NameKey: "studytype.social.name", DescKey: "studytype.social.desc", Education: true, Socioeconomic: true, Religion: true},
	{ID: "sports", NameKey: "studytype.sports.name", DescKey: "studytype.sports.desc", Socioeconomic: true},
	{ID: "tourism", NameKey: "studytype.tourism.name", DescKey: "studytype.tourism.desc", Socioeconomic: true},
	{ID: "media", NameKey: "studytype.media.name", DescKey: "studytype.media.desc", Politics: true, Education: true},
	{ID: "urban", NameKey: "studytype.urban.name", DescKey: "studytype.urban.desc", Socioeconomic: true, Education: true},
	{ID: "energy", NameKey: "studytype.energy.name", DescKey: "studytype.energy.desc", Socioeconomic: true, Education: true, Politics: true},
}

// Get devuelve el tipo por ID (general si no existe).
func Get(id string) Type {
	for _, t := range Catalogue {
		if t.ID == id {
			return t
		}
	}
	return Catalogue[0]
}

// Exists informa si un ID es válido.
func Exists(id string) bool {
	for _, t := range Catalogue {
		if t.ID == id {
			return true
		}
	}
	return false
}
