import {StrictMode} from 'react';
import {createRoot} from 'react-dom/client';
import {App} from './App';
import {ThemedSelectMenus} from './components/ThemedSelectMenus';
import './styles.css';
import './themes.css';

/** Load the workspace UI and its full stylesheet after the HTML loading shell. */
export function mountApplication(host:HTMLElement){
 createRoot(host).render(<StrictMode><App/><ThemedSelectMenus/></StrictMode>);
}
