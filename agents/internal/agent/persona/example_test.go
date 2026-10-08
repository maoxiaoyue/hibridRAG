package persona_test

import (
	"fmt"

	"agent/internal/agent/persona"
)

func ExampleParseResult_ToInstruction() {
	md := `# SupportBot

## Identity
- name: SupportBot
- role: Customer Support Specialist
- domain: E-commerce
- language: zh-TW

## Expression DNA
- tone: warm yet professional
- style: concise, structured

## Mental Models

### First Principles Thinking
Break problems into atomic components.

**Heuristics**
- challenge_assumptions: Ask "why" 3 times
`
	fmt.Print(persona.Parse(md).ToInstruction())

	// Output:
	// You are SupportBot, Customer Support Specialist. Your domain is E-commerce.
	//
	// ## About You
	// - language: zh-TW
	//
	// ## Communication Style
	// - style: concise, structured
	// - tone: warm yet professional
	//
	// ## Mental Models
	//
	// ### First Principles Thinking
	// Break problems into atomic components.
	//
	// Heuristics:
	// - challenge_assumptions: Ask "why" 3 times
}
