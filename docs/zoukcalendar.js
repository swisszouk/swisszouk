var zoukcalendar = (() => {
    const redraw = () => {
        const now = (new Date()).toISOString().slice(0, 10)
        document.querySelectorAll('.event').forEach(el => {
            const evDate = el.getAttribute('data-date')
            if (evDate < now) {
                el.style.display = 'none'
            }
        })
        document.getElementById('content-container').style.visibility = 'visible'
    }

    if (document.readyState == 'loading') {
        document.addEventListener('DOMContentLoaded', redraw)
    } else {
        redraw()
    }

    return { redraw }
})()