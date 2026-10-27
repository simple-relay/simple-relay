const C = {
  ink: '#e8eef8',
  whiteInk: '#f1f7ff',
  muted: '#a8b6cc',
  blue: '#22d3ee',
  sky: '#60a5fa',
  green: '#34d399',
  red: '#fb7185',
  card: '#141c31',
  cardAlt: '#18223a',
  line: '#26324a',
};

const P = {
  sender: { x: 300, y: 500 },
  relay: { x: 960, y: 500 },
  app: { x: 1620, y: 500 },
};

function heading(s, text, _dark, at = 0.1) {
  s.text(text, {
    x: s.cx,
    y: 150,
    size: 66,
    font: 'display',
    color: C.whiteInk,
    align: 'center',
    weight: 600,
    at,
  });
}

function node(s, id, label, icon, x, y, _dark, o = {}) {
  return s.box(label, {
    id,
    x,
    y,
    w: o.w ?? 400,
    h: o.h ?? 190,
    size: o.size ?? 43,
    icon,
    iconSize: 54,
    iconColor: o.iconColor ?? C.blue,
    color: o.stroke ?? C.line,
    fill: o.fill ?? C.card,
    stroke: o.stroke ?? C.line,
    textColor: C.whiteInk,
    at: o.at ?? 0.2,
    out: o.out,
  });
}

export default {
  hook(s) {
    heading(s, 'When webhooks arrive during deploys', false);
    node(s, 'sender', 'Webhook sender', 'cloud', P.sender.x, 500, false, { at: 0.2 });
    node(s, 'app', 'Your application', 'app-window', P.app.x, 500, false, { at: 0.2 });
    s.arrow('sender', 'app', {
      at: '#deploy',
      out: '#response',
      color: C.blue,
      width: 5,
      label: 'webhook',
      labelFont: 'body',
      labelColor: C.ink,
      labelSize: 34,
    });
    s.arrow('app', 'sender', {
      at: '#response',
      color: C.green,
      width: 4,
      label: 'quick response',
      labelFont: 'body',
      labelColor: C.green,
      labelSize: 30,
    });
    s.icon('triangle-alert', { x: P.app.x, y: 720, size: 72, color: C.red, at: '#deploy' });
    s.text('DEPLOYING', { x: P.app.x, y: 795, size: 30, color: C.muted, align: 'center', bold: true, at: '#deploy' });
  },

  setup(s) {
    heading(s, 'Start without extra infrastructure', false);
    node(s, 'relay', 'Simple Relay', 'repeat-2', P.relay.x, 455, false, { at: 0.2, stroke: C.blue });
    node(s, 'sqlite', 'Embedded SQLite', 'database', P.relay.x, 650, false, { at: '#sqlite', h: 145, size: 38, iconSize: 44 });
    s.arrow('relay', 'sqlite', { at: '#sqlite', color: C.sky, width: 4, head: 'end' });
    s.text('One container', { x: 470, y: 755, size: 38, color: C.whiteInk, align: 'center', at: 0.2 });
    s.text('No broker to install', { x: 960, y: 790, size: 42, color: C.blue, align: 'center', weight: 600, at: '#no-broker' });
    s.text('No separate database to provision', { x: 1450, y: 755, size: 34, color: C.muted, align: 'center', maxWidth: 470, at: '#no-broker' });
  },

  ack(s) {
    heading(s, 'Persist first. Acknowledge fast.', false);
    node(s, 'sender', 'Webhook sender', 'cloud', 300, 470, false, { w: 360, h: 175, at: 0.2 });
    node(s, 'relay', 'Simple Relay', 'repeat-2', 960, 470, false, { w: 410, h: 175, at: 0.2, stroke: C.blue });
    node(s, 'app', 'Your application', 'app-window', 1620, 470, false, { w: 360, h: 175, at: 0.2 });
    node(s, 'store', 'Durable storage', 'database', 960, 735, false, { w: 390, h: 145, size: 38, iconSize: 44, at: 0.2 });
    s.text('Signature check, when configured', { x: 960, y: 310, size: 34, color: C.muted, align: 'center', at: '#verify' });
    s.arrow('sender', 'relay', { at: '#verify', out: '#persist', color: C.blue, width: 4 });
    s.arrow('relay', 'store', { at: '#persist', color: C.blue, width: 4, label: 'save original payload', labelFont: 'body', labelColor: C.ink, labelSize: 30 });
    s.text('Payload saved', { x: 960, y: 910, size: 34, color: C.green, align: 'center', weight: 600, at: '#persist' });
    s.arrow('relay', 'sender', { at: '#ack', color: C.blue, width: 5, label: 'ACK', labelFont: 'body', labelColor: C.blue, labelSize: 38 });
    s.text('Your app can recover in the background', { x: 1460, y: 740, size: 34, color: C.muted, align: 'center', maxWidth: 480, at: '#ack' });
  },

  retry(s) {
    heading(s, 'Delivery continues in the background', false);
    node(s, 'relay', 'Simple Relay', 'repeat-2', 500, 485, false, { w: 410, h: 180, at: 0.2, stroke: C.blue });
    node(s, 'app', 'Your application', 'app-window', 1450, 485, false, { w: 430, h: 180, at: 0.2 });
    s.arrow('relay', 'app', { at: '#worker', out: '#retry', color: C.blue, width: 5, label: 'deliver', labelFont: 'body', labelColor: C.ink, labelSize: 34 });
    s.icon('circle-x', { x: 1450, y: 710, size: 66, color: C.red, at: '#retry', out: '#delivered' });
    s.text('Temporarily unavailable', { x: 1450, y: 780, size: 34, color: C.muted, align: 'center', at: '#retry', out: '#delivered' });
    s.icon('rotate-cw', { x: 960, y: 720, size: 90, color: C.sky, at: '#retry' });
    s.text('Bounded backoff', { x: 960, y: 810, size: 36, color: C.ink, align: 'center', at: '#retry' });
    s.icon('circle-check', { x: 1450, y: 710, size: 66, color: C.green, at: '#delivered' });
    s.text('Delivered', { x: 1450, y: 780, size: 34, color: C.ink, align: 'center', at: '#delivered' });
    s.arrow('relay', 'app', { at: '#delivered', color: C.green, width: 5, label: 'retry', labelFont: 'body', labelColor: C.ink, labelSize: 34 });
  },

  recovery(s) {
    heading(s, 'Failures stay visible and recoverable', false);
    node(s, 'request', 'Failed delivery', 'file-warning', 460, 500, false, { at: 0.2, w: 400 });
    node(s, 'dlq', 'Dead letter queue', 'archive-restore', 1120, 500, false, { at: 0.2, w: 440, stroke: C.blue });
    s.arrow('request', 'dlq', { at: '#dlq', color: C.blue, width: 5, label: 'attempt limit reached', labelFont: 'body', labelColor: C.ink, labelSize: 30 });
    s.text('Request retained', { x: 1120, y: 650, size: 34, color: C.muted, align: 'center', at: 'visible', out: '#inspect' });
    s.box('Inspect failure', { id: 'inspect', x: 770, y: 775, w: 390, h: 125, size: 34, icon: 'search', iconSize: 40, fill: C.cardAlt, stroke: C.line, textColor: C.whiteInk, at: '#inspect' });
    s.box('Replay when ready', { id: 'replay', x: 1320, y: 775, w: 430, h: 125, size: 34, icon: 'rotate-cw', iconSize: 40, fill: C.cardAlt, stroke: C.line, textColor: C.whiteInk, at: '#replay' });
    s.arrow('dlq', 'inspect', { at: '#inspect', color: C.muted, width: 3 });
    s.arrow('inspect', 'replay', { at: '#replay', color: C.blue, width: 4 });
  },

  close(s) {
    s.image('assets/logo-dark.svg', { x: 960, y: 270, w: 150, frame: 'none', at: 0.2 });
    s.text('Simple Relay', { x: 960, y: 420, size: 74, color: C.ink, align: 'center', weight: 600, at: 0.2 });
    s.text('Reliable HTTP delivery', { x: 960, y: 515, size: 46, color: C.blue, align: 'center', at: 0.2 });
    s.text('No broker  •  Fast ACK  •  Automatic retries', { x: 960, y: 630, size: 36, color: C.ink, align: 'center', at: '#summary' });
    s.text('Start in about 2 minutes', { x: 960, y: 755, size: 40, color: C.ink, align: 'center', at: '#quickstart' });
    s.box('Read the Quickstart', { x: 960, y: 890, w: 440, h: 110, size: 38, fill: C.blue, stroke: C.blue, textColor: '#0b1020', at: '#quickstart' });
  },
};
