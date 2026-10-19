// Package eval measures extraction quality on a corpus with ground truth: it loads the
// cases of a corpus directory, compares each result with the case's expected.json under
// the case's compare.yaml rules, aggregates the per-case results into a report keyed by
// service_version and diffs two reports.
//
// A corpus is a directory with one subdirectory per case:
//
//	<corpus>/<case>/
//	  document.*        the document (.txt .md .html .eml .pdf .png .jpg .jpeg .webp .heic)
//	  page-*.png|jpg    optional page images, in name order
//	  extractor.md      extractor instructions
//	  judge.md          judge instructions
//	  schema.json       optional JSON Schema; absent means schema-less
//	  expected.json     the ground truth: what a perfect extractor returns
//	  compare.yaml      how each value of expected.json is compared (Rules)
//
// The case id is the directory name. extractor.md, judge.md, schema.json and compare.yaml
// that a case lacks are read from its corpus directory. A case may use the TravelInbox
// layout (source.* and truth.json for document.* and expected.json). A subdirectory that
// is not a case is a nested corpus, one level deep, whose case ids are prefixed with its
// name and "-" (testdata/eval/travelinbox links to the TravelInbox corpus). Running a
// case is the caller's job (a Runner): the command runs it through the engine or over
// HTTP. Nothing in a Report carries document content or result values: case ids,
// statuses, counts and JSON pointers only.
package eval
