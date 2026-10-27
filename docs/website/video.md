# Simple Relay explainer video

## Published video

- YouTube: https://www.youtube.com/watch?v=xzBEIMNnEII
- Landing page embed: `https://www.youtube.com/embed/xzBEIMNnEII?controls=1&rel=0`
- Approximate runtime: 68 seconds
- Format: 16:9, 1920 x 1080

## Message and storyboard

1. **The problem:** A webhook can arrive while an application is deploying and unable to respond.
2. **Quick setup:** Run Simple Relay in one container. No broker to install and no separate database to provision; embedded SQLite stores requests.
3. **Fast ACK:** Optionally verify the signature, persist the original payload, then acknowledge the sender quickly while the application recovers.
4. **Background delivery:** Workers deliver requests and retry temporary failures with bounded backoff.
5. **Recovery:** Failed deliveries remain visible in the dead letter queue and can be inspected and replayed.
6. **Call to action:** Start with one command and be up in about two minutes; read the Quickstart.

The narration is in [`video-source/script.md`](video-source/script.md), and the timed scene visuals are in [`video-source/scenes.js`](video-source/scenes.js). Avoid adding a numeric ACK latency claim; the video only promises a fast acknowledgement.

## Look and audio

- explainroo theme: `midnight`
- Background: `#0b1020`; cards: `#141c31`; cyan: `#22d3ee`; blue: `#60a5fa`; success: `#34d399`; failure: `#fb7185`
- Text: `#e8eef8`; Space Grotesk display font
- Narrator: Kokoro `af_heart` (English)
- Music: `tech`, volume `0.12`; still camera; no watermark or captions
- Logo asset: [`video-source/assets/logo-dark.svg`](video-source/assets/logo-dark.svg)
- Full render was about 68 seconds. `video-source/video.json` holds the explainroo project settings.

## Recreate the video

1. Clone [explainroo](https://github.com/vincentsch/explainroo), install dependencies with `npm install`, and follow its `AGENTS.md` setup instructions (`node bin/explainroo.js doctor --fetch`).
2. Copy this repository's `video-source` directory to `explainroo/videos/simple-relay-explainer` (rename `video-source` to `simple-relay-explainer`).
3. From the explainroo repository, run:

   ```sh
   node bin/explainroo.js voice videos/simple-relay-explainer
   node bin/explainroo.js check videos/simple-relay-explainer
   node bin/explainroo.js sheet videos/simple-relay-explainer
   node bin/explainroo.js render videos/simple-relay-explainer
   node bin/explainroo.js verify videos/simple-relay-explainer
   ```

Review the generated sheet before rendering. The render creates the MP4, subtitle files, and cover image in the explainroo video project.
