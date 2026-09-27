/** @type {import('tailwindcss').Config} */
// Same theme the panel used through the Tailwind Play CDN.
export default {
  content: ['./index.html', './src/**/*.{vue,ts}'],
  theme: {
    extend: {
      fontFamily: { sans: ['Inter', 'sans-serif'] },
      colors: {
        darkBg: '#090a0f',
        glassBg: 'rgba(15, 23, 42, 0.45)',
        glassBorder: 'rgba(255, 255, 255, 0.08)',
        brandTeal: '#0ea5e9',
        brandIndigo: '#6366f1',
      },
    },
  },
  plugins: [],
}
