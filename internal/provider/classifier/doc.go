// Package classifier implements provider.Provider for classifier models that answer a
// fixed set of typed questions with calibrated probabilities instead of generating text
// (ADR 0004, decision 14). Such a model can serve only the judge role.
//
// The adapter speaks a small generic HTTP contract, documented in
// docs/providers/classifier.md, that a vendor adapter or a thin proxy in front of a
// vendor API must satisfy: POST {base_url}/classify with the questions and the text,
// answered by one decision per question. It renders the decisions as the judge verdict
// the extraction loop parses, so the loop needs no classifier-specific parsing: accept
// when every question's probability reaches its threshold, confidence the minimum
// probability, and on reject one feedback entry at pointer "" (the whole result), which
// roles.judge.feedback_fallback: llm replaces with an LLM's pointer-level feedback.
package classifier
