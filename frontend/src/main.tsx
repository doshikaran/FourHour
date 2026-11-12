import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { createDefaultApi } from './api/context';
import './styles.css';
import './pages/presentation.css';

const root = document.getElementById('root');
if (!root) throw new Error('index.html has no #root element');

createRoot(root).render(
  <StrictMode>
    <App api={createDefaultApi()} />
  </StrictMode>,
);
