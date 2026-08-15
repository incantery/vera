<script>
	// Assistant text rendered as the markdown it is — parsed by
	// marked, sanitized by DOMPurify (agent output is untrusted input;
	// it becomes HTML only after the wash). Styles live in app.css
	// under .md, chat-scale: quiet headings, real code blocks, no
	// document-sized margins.
	import { marked } from 'marked';
	import DOMPurify from 'dompurify';

	let { text = '' } = $props();
	const html = $derived(
		DOMPurify.sanitize(marked.parse(text, { gfm: true, breaks: true, async: false }))
	);
</script>

<div class="md">{@html html}</div>
