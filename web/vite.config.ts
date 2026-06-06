import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { VitePWA } from 'vite-plugin-pwa';

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    VitePWA({
      registerType: 'autoUpdate',
      includeAssets: ['favicon.ico', 'apple-touch-icon-180x180.png'],
      manifest: {
        name: 'WeSync',
        short_name: 'WeSync',
        description: 'Sync files between your devices',
        theme_color: '#0099cc',
        background_color: '#f8fafc',
        display: 'standalone',
        orientation: 'portrait',
        start_url: '/',
        scope: '/',
        icons: [
          { src: 'pwa-64x64.png', sizes: '64x64', type: 'image/png' },
          { src: 'pwa-192x192.png', sizes: '192x192', type: 'image/png' },
          { src: 'pwa-512x512.png', sizes: '512x512', type: 'image/png', purpose: 'any' },
          {
            src: 'maskable-icon-512x512.png',
            sizes: '512x512',
            type: 'image/png',
            purpose: 'maskable',
          },
        ],
      },
      workbox: {
        // Cache app shell — API calls go straight to network.
        globPatterns: ['**/*.{js,css,html,svg,png,ico,woff2}'],
        // Exclude /api/* from the SPA navigation fallback so direct requests
        // to /api/status etc. reach the Go server, not the cached index.html.
        navigateFallbackDenylist: [/^\/api\//],
        runtimeCaching: [
          {
            // WeSync API — always network, never cache.
            urlPattern: /^.*\/api\/.*/,
            handler: 'NetworkOnly',
          },
        ],
      },
    }),
  ],
  server: {
    proxy: {
      '/api': {
        target: 'https://localhost:47820',
        secure: false,
        ws: true,
      },
    },
  },
  test: {
    environment: 'node',
    exclude: ['e2e/**', '**/node_modules/**'],
  },
});
