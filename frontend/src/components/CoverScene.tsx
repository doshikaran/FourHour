import { useRef, useState, type PointerEvent } from 'react';
import { Link } from '../lib/router';
import './CoverScene.css';

/** A CSS 3D illustration of the two signals behind a claim. */
export function CoverScene() {
  const scene = useRef<HTMLDivElement>(null);
  const [paused, setPaused] = useState(false);

  function tilt(event: PointerEvent<HTMLDivElement>) {
    if (event.pointerType !== 'mouse' || paused) return;
    const bounds = event.currentTarget.getBoundingClientRect();
    scene.current?.style.setProperty('--tilt-x', `${(0.5 - (event.clientY - bounds.top) / bounds.height) * 9}deg`);
    scene.current?.style.setProperty('--tilt-y', `${((event.clientX - bounds.left) / bounds.width - 0.5) * 12}deg`);
  }

  function resetTilt() {
    scene.current?.style.setProperty('--tilt-x', '0deg');
    scene.current?.style.setProperty('--tilt-y', '0deg');
  }

  return (
    <aside className={`cover-scene ${paused ? 'is-paused' : ''}`} aria-label="Illustration of county and witness signals supporting automatic cover">
      <div className="scene-header">
        <span>POWER OUT. COVER ON.</span>
        <button className="scene-motion" type="button" aria-pressed={paused} onClick={() => { setPaused(!paused); resetTilt(); }}>
          {paused ? 'Play motion' : 'Pause motion'}
        </button>
      </div>
      <div className="scene-stage" onPointerMove={tilt} onPointerLeave={resetTilt}>
        <div className="scene-world" ref={scene} aria-hidden="true">
          <div className="scene-floor" />
          <div className="scene-ring scene-ring-back" />
          <div className="scene-disc">
            <div className="disc-edge" />
            <div className="disc-face">
              <span className="disc-caption">FOURHOUR</span>
              <strong>4<span>h</span></strong>
              <span className="disc-bottom">EVERY HOUR COUNTS</span>
            </div>
          </div>
          <div className="scene-ring scene-ring-front" />
          <div className="signal-tile county-tile">
            <span className="tile-icon">↗</span>
            <span>County signal</span>
            <div className="signal-bars">{[18, 29, 21, 40, 31, 47, 39, 53, 45].map((height, index) => <i key={index} style={{ height, animationDelay: `${index * 100}ms` }} />)}</div>
          </div>
          <div className="signal-tile witness-tile">
            <span className="tile-icon witness-icon">⌁</span>
            <span>Your witness</span>
            <div className="witness-line"><i /><i /><i /><i /><i /></div>
            <span className="tile-caption">A signed power log</span>
          </div>
        </div>
      </div>
      <div className="scene-description">
        <p>Local evidence.<br /><strong>Automatic action.</strong></p>
        <span>County outage data and your device<br />work together to decide a claim.</span>
      </div>
      <Link to="/live" className="scene-link">Explore the live replay <span aria-hidden="true">↗</span></Link>
    </aside>
  );
}
