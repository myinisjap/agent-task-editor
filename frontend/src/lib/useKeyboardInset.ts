import { useEffect, useState } from 'react'

// Height in px of the on-screen keyboard overlapping the layout viewport.
//
// Mobile browsers (iOS Safari, and Chrome with the default
// `interactive-widget=resizes-visual`) shrink the *visual* viewport when the
// keyboard opens but leave the layout viewport — and therefore `h-dvh` and
// every element's box — untouched. Anything anchored to the bottom of the page
// ends up hidden behind the keyboard. Padding the page by this value keeps it
// visible. Browsers that do resize the layout viewport report ~0 here, so the
// padding is a no-op there.
export function useKeyboardInset(): number {
  const [inset, setInset] = useState(0)

  useEffect(() => {
    const vv = window.visualViewport
    if (!vv) return
    const update = () => {
      const overlap = window.innerHeight - (vv.height + vv.offsetTop)
      // Sub-pixel rounding noise shows up as a 0.5px overlap with no keyboard.
      setInset(overlap > 1 ? Math.round(overlap) : 0)
    }
    update()
    // scroll matters too: panning the visual viewport changes offsetTop.
    vv.addEventListener('resize', update)
    vv.addEventListener('scroll', update)
    return () => {
      vv.removeEventListener('resize', update)
      vv.removeEventListener('scroll', update)
    }
  }, [])

  return inset
}
