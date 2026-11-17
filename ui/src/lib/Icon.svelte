<script>
  // Line icons drawn in place (no icon font, no emoji). 24 px grid, stroke follows the text color.
  let { name, size = 18 } = $props()

  const paths = {
    chat: 'M4 5.5A2.5 2.5 0 0 1 6.5 3h11A2.5 2.5 0 0 1 20 5.5v8a2.5 2.5 0 0 1-2.5 2.5H10l-4.5 4v-4H6.5A2.5 2.5 0 0 1 4 13.5z',
    overview: 'M4 20V10M10 20V4M16 20v-7M22 20H2',
    approvals: 'M12 3 4.5 6v5.5c0 4.6 3.2 8 7.5 9.5 4.3-1.5 7.5-4.9 7.5-9.5V6zM8.8 12l2.2 2.2 4.4-4.4',
    memory: 'M12 3 3 7.5l9 4.5 9-4.5zM3 12l9 4.5 9-4.5M3 16.5 12 21l9-4.5',
    jobs: 'M12 7v5l3 2M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0z',
    activity: 'M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01',
    settings: 'M4 7h10M18 7h2M4 17h4M12 17h8M14 4.5v5M8 14.5v5',
    plus: 'M12 5v14M5 12h14',
    send: 'M5 12h13M13 6l6 6-6 6',
    stop: 'M7 7h10v10H7z',
    speaker: 'M11 5 6 9H3v6h3l5 4zM15.5 8.5a5 5 0 0 1 0 7M18.5 5.5a9 9 0 0 1 0 13',
    mute: 'M11 5 6 9H3v6h3l5 4zM16 9.5l5 5M21 9.5l-5 5',
    attach: 'M20 11.5 12.2 19.3a5 5 0 0 1-7.1-7.1l8.5-8.5a3.3 3.3 0 0 1 4.7 4.7l-8.5 8.5a1.7 1.7 0 0 1-2.4-2.4l7.8-7.8',
    file: 'M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8zM14 3v5h5M9 13h6M9 17h6',
    image: 'M5 4h14a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1zM4 16l4.5-4.5 4 4 2.5-2.5L20 18M15.5 9.5h.01',
    menu: 'M4 7h16M4 12h16M4 17h16',
    more: 'M5 12h.01M12 12h.01M19 12h.01',
    search: 'M11 18a7 7 0 1 1 0-14 7 7 0 0 1 0 14zM20 20l-4-4',
    chevron: 'M9 6l6 6-6 6',
    down: 'M6 9l6 6 6-6',
    close: 'M6 6l12 12M18 6 6 18',
    tool: 'M14.5 6.5a4 4 0 0 0-5.3 5.3L4 17v3h3l5.2-5.2a4 4 0 0 0 5.3-5.3l-2.5 2.5-2.3-.7-.7-2.3z',
    warn: 'M12 4 2.5 20h19zM12 10v4M12 17h.01',
    check: 'M5 12.5l4.5 4.5L19 7.5',
    spark: 'M12 3v4M12 17v4M3 12h4M17 12h4M6 6l2.5 2.5M15.5 15.5 18 18M6 18l2.5-2.5M15.5 8.5 18 6',
    edit: 'M4 20h4L19 9l-4-4L4 16zM13.5 6.5l4 4',
    trash: 'M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3',
    play: 'M8 5v14l11-7z',
    bell: 'M6 16V11a6 6 0 1 1 12 0v5l1.5 2h-15zM10 20a2 2 0 0 0 4 0',
    server: 'M4 5h16v6H4zM4 13h16v6H4zM8 8h.01M8 16h.01',
    mic: 'M12 3a3 3 0 0 0-3 3v6a3 3 0 0 0 6 0V6a3 3 0 0 0-3-3zM5.5 11a6.5 6.5 0 0 0 13 0M12 17.5V21',
    'mic-off': 'M12 3a3 3 0 0 0-3 3v6a3 3 0 0 0 6 0V6a3 3 0 0 0-3-3zM5.5 11a6.5 6.5 0 0 0 13 0M12 17.5V21M4 4l16 16',
    camera: 'M3 7.5h3.5L8.5 5h7l2 2.5H21V19H3zM12 16a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7z',
    'camera-off': 'M3 7.5h3.5L8.5 5h7l2 2.5H21V19H3zM12 16a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7zM3 3l18 18',
    eye: 'M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12zM12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z',
    live: 'M4 10v4M8 7v10M12 4v16M16 8v8M20 11v2',
    sidebar: 'M4 4.5h16v15H4zM9.5 4.5v15',
    expand: 'M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5',
    collapse: 'M9 4v5H4M15 4v5h5M9 20v-5H4M15 20v-5h5',
  }
</script>

<svg class="icon" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"
  stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d={paths[name] || ''} /></svg>
