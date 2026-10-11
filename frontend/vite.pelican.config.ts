import { defineConfig } from 'vite'
import { resolve } from 'path'

// Also embedded into API preview_html: no remote assets or client framework.
export default defineConfig({
  build: {
    outDir: '../backend/internal/web/dist',
    emptyOutDir: false,
    lib: {
      entry: resolve(__dirname, 'src/utils/pelicanRenderer.ts'),
      name: 'Sub2APIPelican',
      formats: ['iife'],
      fileName: () => 'pelican-renderer.js',
    },
    // Serialized measurement scripts must stay self-contained after bundling.
    minify: false,
  },
})
