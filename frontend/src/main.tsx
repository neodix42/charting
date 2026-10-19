import {StrictMode} from 'react';
import {createRoot} from 'react-dom/client';
import {App} from './App';
import './styles.css';
// Test-only bridge is never enabled without an explicit environment flag.
if(import.meta.env.VITE_EXPLICIT_TEST_BRIDGE==='1'){const {installTestBridge}=await import('./test/browserBridge');await installTestBridge();}
createRoot(document.getElementById('root')!).render(<StrictMode><App/></StrictMode>);
