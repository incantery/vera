// Pure SPA: no server rendering, no prerender — the Go binary serves
// one shell and the page talks to /api from there.
export const ssr = false;
export const prerender = false;
export const csr = true;
