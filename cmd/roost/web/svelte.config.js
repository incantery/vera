import adapter from '@sveltejs/adapter-static';

/** @type {import('@sveltejs/kit').Config} */
export default {
	kit: {
		// Pure SPA: one index.html fallback, embedded into the Go binary.
		adapter: adapter({ fallback: 'index.html' })
	}
};
