import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  // the API (go run ./cmd/api -addr 127.0.0.1:55453 -config dev/hareno.yaml) behind the dev server
  server: { proxy: { '/api': 'http://127.0.0.1:55453', '/auth': 'http://127.0.0.1:55453' } },
})
