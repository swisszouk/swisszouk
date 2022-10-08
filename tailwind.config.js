/** @type {import('tailwindcss').Config} */

module.exports = {
  content: ["./_build/*.{html,js}"],
  theme: {
    extend: {
      colors: {
        background: '#cabea3',
        eventbg: 'white',
        title: 'white',
        border: 'white',
        main: 'black',
        gradient_left: '#01A0E2',
        gradient_mid: '#F0CA2B',
        gradient_right: '#B3318B'
      }
    }
  },
  plugins: [],
}