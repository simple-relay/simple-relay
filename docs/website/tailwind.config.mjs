import typography from '@tailwindcss/typography';

/** @type {import('tailwindcss').Config} */
export default {
  content: ['./src/**/*.{astro,html,js,jsx,md,mdx,svelte,ts,tsx,vue}'],
  theme: {
    extend: {
      fontFamily: {
        sans: ['Inter', 'system-ui', '-apple-system', 'BlinkMacSystemFont', 'sans-serif'],
        mono: ['JetBrains Mono', 'Fira Code', 'Menlo', 'monospace'],
      },
      colors: {
        // Apple-inspired design system from DESIGN.md
        primary: '#0066cc',
        'primary-focus': '#0071e3',
        'primary-dark': '#2997ff',   // for dark tiles
        canvas: '#ffffff',
        parchment: '#f5f5f7',
        pearl: '#fafafc',
        ink: '#1d1d1f',
        'ink-muted': '#6e6e73',
        'ink-muted-48': '#7a7a7a',
        'tile-1': '#272729',
        'tile-2': '#2a2a2c',
        'tile-3': '#1d1d1f',
        hairline: '#d2d2d7',
        divider: '#e8e8ed',
      },
      fontSize: {
        'hero': ['56px', { lineHeight: '1.07', letterSpacing: '-0.28px', fontWeight: '600' }],
        'display-lg': ['40px', { lineHeight: '1.10', letterSpacing: '0', fontWeight: '600' }],
        'display-md': ['34px', { lineHeight: '1.2', letterSpacing: '-0.374px', fontWeight: '600' }],
        'lead': ['21px', { lineHeight: '1.4', letterSpacing: '0', fontWeight: '400' }],
        'tagline': ['21px', { lineHeight: '1.19', letterSpacing: '0.231px', fontWeight: '600' }],
        'body-relay': ['17px', { lineHeight: '1.47', letterSpacing: '-0.374px', fontWeight: '400' }],
        'caption': ['14px', { lineHeight: '1.43', letterSpacing: '-0.224px', fontWeight: '400' }],
      },
      spacing: {
        'section': '80px',
      },
      borderRadius: {
        'pill': '9999px',
        'card': '18px',
      },
      boxShadow: {
        'product': '0 3px 30px 5px rgba(0, 0, 0, 0.22)',
      },
      letterSpacing: {
        'tight-display': '-0.028em',
        'tight-body': '-0.022em',
      },
      animation: {
        'scroll': 'scroll 40s linear infinite',
        'flow-right': 'flowRight 2s ease-in-out infinite',
        'pulse-dot': 'pulseDot 2s ease-in-out infinite',
      },
      keyframes: {
        scroll: {
          '0%': { transform: 'translateX(0)' },
          '100%': { transform: 'translateX(-50%)' },
        },
        flowRight: {
          '0%, 100%': { opacity: '0.3', transform: 'translateX(0)' },
          '50%': { opacity: '1', transform: 'translateX(4px)' },
        },
        pulseDot: {
          '0%, 100%': { opacity: '0.4', transform: 'scale(1)' },
          '50%': { opacity: '1', transform: 'scale(1.2)' },
        },
      },
    },
  },
  plugins: [typography],
};
