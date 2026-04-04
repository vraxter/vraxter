package coders

// SkillCoder defines the interface for language-specific skill compilers
type SkillCoder interface {
	Compile(name, description, code string) error
	Language() string
}
