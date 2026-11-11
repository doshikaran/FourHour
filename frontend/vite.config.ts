/// <reference types="vitest/config" />
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Environment variables come from the repo-root .env (CONTRACTS.md section 2), so envDir is '..'.
export default defineConfig({
  plugins: [react()],
  envDir: '..',
  server: { port: 5173 },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
    restoreMocks: true,
  },
});
