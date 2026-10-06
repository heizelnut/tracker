# Ricomposizione — Progress Tracker

Progetto inizializzato a partire dalla conversazione ChatGPT
[_"Programma palestra 3 giorni"_](https://chatgpt.com/share/6ac499e1-17a8-83eb-89f8-8048abe883e5).

L'obiettivo è la **ricomposizione corporea**: costruire/recuperare massa muscolare
riducendo gradualmente il grasso, con un deficit calorico moderato.

## Cosa contiene

| File | Descrizione |
|---|---|
| [`index.html`](./index.html) | App web (single file) per tracciare peso e calorie. Salva tutto in **LocalStorage**, nessun server. |
| [`docs/scheda-allenamento.md`](./docs/scheda-allenamento.md) | Scheda palestra 3 giorni (2× upper / 2× lower). |
| [`docs/piano-nutrizionale.md`](./docs/piano-nutrizionale.md) | Target calorici e macro (giorni allenamento vs riposo). |

## Profilo di riferimento

- 22 anni, maschio
- 172 cm, 72,7 kg
- Vita sedentaria + 3 allenamenti/settimana
- ~25 min di cardio per sessione (15' prima + 10' dopo)
- Obiettivo: ricomposizione **abbastanza aggressiva**

## Come usare il tracker

Apri `index.html` nel browser (anche da smartphone Android). Puoi servirlo
localmente con:

```bash
# Python
python3 -m http.server 8000

# oppure Node
npx serve .
```

Poi visita <http://localhost:8000>.

### Funzionalità

- 📊 Andamento del peso con grafico
- ⚖️ Peso giornaliero
- 🔥 Calorie introdotte
- 🏋️ Distinzione allenamento / riposo
- 📈 Medie degli ultimi 7 giorni
- 🗂️ Storico completo
- 💾 Salvataggio automatico in LocalStorage
- 🗑️ Eliminazione singola registrazione o di tutto lo storico
- 📱 Interfaccia responsive per Android

> I dati restano **sul dispositivo/browser** e non vengono inviati a nessun server.

## Prossimi passi possibili

- [ ] Tracciamento macro giornalieri (proteine, carboidrati, grassi)
- [ ] Promemoria settimanale per registrare peso e calorie
- [ ] Export dei dati in formato CSV
