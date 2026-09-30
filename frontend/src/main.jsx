import React from 'react';
import { createRoot } from 'react-dom/client';
import { ThemeProvider } from './store/theme.jsx';
import { UiProvider } from './store/ui.jsx';
import { SessionProvider } from './store/session.jsx';
import { EventsProvider } from './store/events.jsx';
import { consumeDeepLink } from './lib/router.js';
import { getStartParam } from './lib/max.js';
import App from './App.jsx';
import './styles/theme.css';
import './styles/app.css';

// /app/{event_id} из бота или start_param → карточка мероприятия.
consumeDeepLink(getStartParam());

createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <ThemeProvider>
      <UiProvider>
        <SessionProvider>
          <EventsProvider>
            <App />
          </EventsProvider>
        </SessionProvider>
      </UiProvider>
    </ThemeProvider>
  </React.StrictMode>,
);
