var zoukcalendar = (() => {
    let city = 'Zürich'
    try {
        city = localStorage.getItem('city') || 'Zürich'
    } catch (e) {
        console.warn(`could not read localstorage, showing zrh: ${e}`)
    }
    const menu = document.getElementById('menu')
    const burger = document.getElementById('burger')
    let menuVisible = false;
    window.toggleMenu = () => {
        menuVisible = !menuVisible;
        // menu.style.display = menuVisible ? 'flex' : 'none';
        menu.style.maxHeight = menuVisible ? '10em' : '0px';
    }
    window.selectCity = (c) => {
        console.log('select ' + c)
        city = c
        toggleMenu()
        redraw()
        try {
            localStorage.setItem('city', c)
        } catch {}
        // This is kind of ugly: if we're on about page,
        // navigate back to the event list.
        if (!location.href.endsWith("index.html")) {
            window.location = "index.html"
        }
    }
    const redraw = () => {
        burger.innerText = city
        if (city === 'all') {
            burger.innerText = 'Select city'
        }

        const n = new Date()
        const now = (new Date()).toISOString().slice(0, 10)
        let timeout = new Date()
        timeout.setDate(timeout.getDate() + 30)
        timeout = timeout.toISOString().slice(0, 10)
        console.log(`timeout: ${timeout}`)
        document.querySelectorAll('.event').forEach(el => {
            const evDate = el.getAttribute('data-date')
            const evCity = el.getAttribute('data-city')
            let display = 'block'
            if (evDate < now) {
                display = 'none'
            }
            let cityMatches = city === 'all' ||
              city === evCity ||
              city === 'Genève/Lausanne' && (evCity === 'Genève' || evCity === 'Lausanne')
            if (!cityMatches) {
                display = 'none'
            }
            el.style.display = display
        })
        document.querySelectorAll('.location-city').forEach(el => {
            el.style.display = city === 'all' ? 'inline' : 'none'
        })
        document.getElementById('content-container').style.display = 'block'
    }

    if (document.readyState == 'loading') {
        document.addEventListener('DOMContentLoaded', redraw)
    } else {
        redraw()
    }

    return { redraw }
})()