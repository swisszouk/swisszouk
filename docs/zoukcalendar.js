var zoukcalendar = (() => {
    const redraw = () => {
        const n = new Date()
        const now = (new Date()).toISOString().slice(0, 10)
        let timeout = new Date()
        timeout.setDate(timeout.getDate() + 30)
        timeout = timeout.toISOString().slice(0, 10)
        console.log(`timeout: ${timeout}`)
        document.querySelectorAll('.event').forEach(el => {
            const evDate = el.getAttribute('data-date')
            if (evDate < now) {
                el.style.display = 'none'
            }
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