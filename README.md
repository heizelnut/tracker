# Ricomposizione — Progress Tracker

Progetto inizializzato a partire dalla conversazione ChatGPT
[_"Programma palestra 3 giorni"_](https://chatgpt.com/share/6ac499e1-17a8-83eb-89f8-8048abe883e5).

L'obiettivo è la **ricomposizione corporea**: costruire/recuperare massa muscolare
riducendo gradualmente il grasso, con un deficit calorico moderato.

## Cosa contiene

| File | Descrizione |
|---|---|
| [`index.html`](./index.html) | App web (single file) per tracciare peso, calorie e macro. Salva tutto in **LocalStorage**. |
| [`cmd/server`](./cmd/server) | Backend (Go + SQLite) che serve l'app, sincronizza i record in modo incrementale, espone un server **MCP** per gli agenti e mantiene la vecchia API a blob per la migrazione. |
| [`Dockerfile`](./Dockerfile) · [`docker-compose.yml`](./docker-compose.yml) | Self-hosting in un container. |
| [`manifest.json`](./manifest.json) · [`sw.js`](./sw.js) · [`favicon.svg`](./favicon.svg) · [`icons/`](./icons) | PWA: icona, installabile sulla home screen, funziona offline. |
| [`data/cibi.csv`](./data/cibi.csv) | Database alimenti d'esempio, esportato dal foglio **“Cibo”** di `~/Documents/dieta/macros.xlsx`. |
| [`data/allenamenti.csv`](./data/allenamenti.csv) | Scheda allenamento 3 giorni d'esempio (dalla conversazione originale). |
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

L'interfaccia è divisa in **tab**:

- **Dashboard** — peso attuale, medie 7 giorni (peso, kcal, P/G/C), nuova registrazione, grafico del peso.
- **Diario** — alimenti consumati per data, con grammi e totali kcal/macro vs target. Le voci si possono **riordinare** (trascina l'handle `⠿` oppure usa `▲`/`▼`).
- **Allenamenti** — programma diviso per sessioni, aggiungi/modifica manualmente o importa da CSV.
- **Cibi** — database alimenti: aggiungi/modifica manualmente, import/export CSV, ricerca e filtro per categoria.
- **Storico** — tutte le registrazioni con peso, kcal, macro e tipo giornata.
- **Impostazioni** — backup (esporta/importa/cancella) e sincronizzazione.

In dettaglio:

- 📊 Andamento del peso con grafico
- ⚖️ Peso giornaliero
- 🔥 Calorie introdotte
- 🏋️ Distinzione allenamento / riposo
- 📈 Medie degli ultimi 7 giorni (peso, kcal, proteine, grassi, carboidrati)
- 🗂️ Storico completo con macro
- 💾 Salvataggio automatico in LocalStorage
- 🗑️ Eliminazione singola registrazione o di tutto lo storico
- 📱 Interfaccia responsive per Android
- 📲 **PWA**: installabile sulla home screen (standalone, con icona) e utilizzabile offline
- 🥩 **Database cibi** importabile da CSV (vedi sotto)
- ➕ **Aggiunta/modifica manuale degli alimenti** dall'interfaccia
- 📓 **Diario alimentare**: aggiungi gli alimenti con i grammi e l'app calcola kcal + macro vs target
- ↕️ **Riordino voci del diario**: trascina l'handle `⠿` (desktop) o usa i pulsanti `▲`/`▼` (anche su mobile) per cambiare l'ordine dei pasti nella giornata
- 🤖 **Suggerimento greedy**: cosa mangiare nel resto della giornata per completare kcal e macro

> I dati restano **sul dispositivo/browser** e non vengono inviati a nessun server.

## ➕ Aggiungere un alimento a mano

Nel tab **Cibi**, il form in alto permette di creare un alimento con:

| Campo | Note |
|---|---|
| Nome | obbligatorio |
| Proteine / Grassi / Carboidrati | g per 100 g |
| kcal | se lasciato vuoto viene calcolata da `P×4 + C×4 + G×9` |
| Min / Max | porzione consigliata in grammi (opzionale) |
| Categoria | tag separati da virgola, es. `proteina,latticino` |
| Pezzo | grammi per pezzo (opzionale) |

Ogni riga della tabella ha i pulsanti **Aggiungi** (al diario), **Modifica** e **×** (elimina).

## 🥩 Importare un CSV di cibi

Nel database cibi puoi **importare un CSV** con la stessa struttura del secondo
foglio (`Cibo`) di `~/Documents/dieta/macros.xlsx`:

```csv
Food (100gr);Proteine;Grasso;Carboidrati;kcal;Min (g);Max (g);Categoria;Pezzo (g)
Grana Padano;33;29;0;398;10;30;extra,secco;
Petto di pollo;22,5;2,6;0;120;120;280;proteina;
```

- I valori sono **per 100 g**.
- Delimitatore `,`, `;` o tab: rilevato automaticamente.
- Decimali con `.` o `,`.
- L'intestazione viene riconosciuta anche se il file ha un preambolo (es. `macros.csv`).
- Se `kcal` manca viene calcolata con `P×4 + C×4 + G×9`.
- Le colonne `Min`, `Max`, `Categoria` e `Pezzo` sono opzionali.
- L'import **unisce** i dati: alimenti con lo stesso nome vengono aggiornati, gli altri aggiunti.

Il pulsante **“Carica alimenti di esempio”** inserisce i 32 alimenti di
[`data/cibi.csv`](./data/cibi.csv) senza bisogno di importare nulla.
Puoi anche **esportare** il database corrente o **scaricare un modello** CSV vuoto.

## 🏋️ Allenamenti (CSV)

Il tab **Allenamenti** gestisce la scheda divisa per **sessioni** (giorni). Ogni
esercizio ha: Sessione, Esercizio, Serie, Tipo, Recupero, Note. Il **Tipo**
distingue gli esercizi **compound** (multi-articolari) da quelli di **isolation**;
Serie e Recupero sono testo libero, così funzionano sia `3` sia `2-3'`.

- **Carica programma di esempio** inserisce la scheda 3 giorni della conversazione
  originale (22 esercizi, `data/allenamenti.csv`).
- **Importa CSV** con questa struttura:

```csv
Sessione;Esercizio;Serie;Tipo;Recupero;Note
Giorno 1 — Upper A;Spinta manubri — panca piana;3;compound;2-3';
Giorno 1 — Upper A;Alzate laterali;3;isolation;60-90";
```

- Delimitatore `,` o `;` auto-rilevato; intestazione riconosciuta automaticamente.
- `Tipo` accetta `compound`/`isolation` (anche `multi`/`iso`).
- Colonne `Sessione`, `Tipo` e `Note` opzionali.
- L'import **unisce**: stessa sessione + esercizio = aggiorna, altrimenti aggiunge.
- Puoi anche **esportare** il programma o scaricare un **modello** CSV.
- **⏱ Timer recupero fullscreen**: ogni esercizio ha un pulsante che avvia un countdown
a schermo intero col tempo di recupero ricavato dalla colonna *Recupero*
(`2-3'` → 180s, `60-90"` → 90s, `1'30"` → 90s). Include pausa, ±15s, anello di
progresso, beep e vibrazione a fine recupero. Esc/Chiudi per uscire.

## 🤖 Algoritmo greedy (score per macro)

Nel tab **Diario**, sotto i totali, l'app suggerisce **cosa mangiare per completare
la giornata** valutando **tutta la tabella dei cibi**:

1. Calcola il fabbisogno residuo `target − consumato` per kcal, proteine, grassi e carboidrati.
2. Per ogni alimento, con la sua porzione consigliata (`Max`, altrimenti `Min`, altrimenti 100 g),
   calcola uno **score per macro**: quanto la porzione copre il residuo di quel macro
   (100% = lo copre esattamente; chi sfora viene penalizzato con `1/ratio`).
3. Combina gli score in un **punteggio totale**, pesando di più i macro più carenti e in
   particolare le proteine (pesi: P 0.40, C 0.25, G 0.20, kcal 0.15).
4. Ordina e restituisce i **primi 10** alimenti, con la possibilità di riordinare per
   singolo macro (proteine / grassi / carboidrati / calorie) ed escludere i cibi già nel diario.

Ogni riga mostra lo score e i grammi apportati per ciascun macro, più un pulsante
**Aggiungi** per inserire la porzione nel diario.

## 🐳 Self-hosting (docker compose)

Il repo include un backend in **Go** che serve l'app e conserva i dati in
**SQLite** (una riga per record). I client sincronizzano in modo **incrementale**
(`/api/sync`) e possono ricevere gli aggiornamenti in **tempo reale** via SSE
(`/api/events`). Niente autenticazione: è pensato per girare in **LAN / dietro
Tailscale**, non esposto su internet.

> **Fase di migrazione.** La vecchia API a blob opaco (`/api/kv`) resta attiva
> come *vista* su SQLite: un `GET` ricostruisce il blob dai record, un `PUT` lo
> importa record per record. Così i browser che usano ancora il client
> precedente migrano da soli al primo sync, senza perdere dati e senza cambiare
> nulla. Il vecchio file blob viene letto una sola volta (e conservato come
> backup); quando tutti i client useranno `/api/sync`, `/api/kv` potrà sparire.

### Avvio rapido

```bash
cp .env.example .env
docker compose up --build
# apri http://localhost:8080
```

### API

| Metodo | Path | Descrizione |
|---|---|---|
| `GET` | `/api/sync/{key}?since=TS` | Delta incrementale: record e tombstone con `updated > TS` |
| `POST` | `/api/sync/{key}` | Invia le modifiche locali (last-write-wins) |
| `GET` | `/api/events/{key}` | Stream **SSE** con le modifiche in tempo reale |
| `GET` | `/api/kv/{key}` | *(compat)* Blob ricostruito dai record |
| `PUT` | `/api/kv/{key}` | *(compat/migrazione)* Importa un blob record per record |
| `DELETE` | `/api/kv/{key}` | *(compat)* Elimina tutti i record della chiave |
| `POST` | `/mcp` | Server MCP (Streamable HTTP, JSON-RPC 2.0) per gli agenti |
| `GET` | `/healthz` | Healthcheck |
| `*` | `/` | Serve l'app statica (`index.html`) |

I dati finiscono in `DATA_DIR/tracker.db` (SQLite, WAL). Le chiavi sono
`[A-Za-z0-9_-]{1,64}` e isolano i dataset tra loro.

#### Record e merge

Ogni record è `(collection, id, data, updated, deleted)`:

| Collection | id | data |
|---|---|---|
| `entries` | data (`YYYY-MM-DD`) | peso, calorie, tipo giornata |
| `foods` | id alimento | nome, macro per 100 g, … |
| `diary` | data | tipo giornata + voci del diario |
| `workouts` | id esercizio | sessione, serie, ripetizioni, … |
| `done` | `data\0id` | spunta di un esercizio in un giorno |

Il merge è **last-write-wins** sul timestamp del client; a parità vince la
*tombstone*, altrimenti la modifica in arrivo. Salvare una modifica è una singola
`UPSERT`: non si riscrive più tutto il blob.

#### gzip

La compressione è solo di trasporto: la risposta di `/api/kv` viene compressa se
il client manda `Accept-Encoding: gzip`, e un upload gzip viene decompresso
prima dell'import. Non c'è più un blob compresso "a riposo": lo storage è
SQLite.

### Sync nell'app

La sincronizzazione è **attiva di default** (stesso server che serve l'app, chiave
`default`): non serve configurarla. Il pannello **Impostazioni** mostra lo stato,
permette di forzare **Sincronizza ora** / **Invia al server** / **Ricarica dal
server** e, se serve, di puntare a un altro server o a un'altra chiave.

La sincronizzazione è **per record**, non a blob intero:

- ogni record (peso/calorie per data, giornata di diario, alimento o esercizio
  per id, singola spunta di allenamento) porta un timestamp di ultima modifica;
- al sync l'app chiede solo le modifiche più recenti del suo ultimo pull
  (`GET /api/sync/{key}?since=…`) e invia solo i record in attesa in una coda
  locale (*outbox*): niente più blob interi da riscrivere;
- gli aggiornamenti degli altri dispositivi arrivano anche **in tempo reale**
  via SSE (`GET /api/events/{key}`), senza aspettare il prossimo sync;
- un dispositivo rimasto indietro **non può più cancellare** le giornate
  registrate su un altro dispositivo: le unisce;
- le eliminazioni viaggiano come *tombstone*, così non vengono resuscitate da
  una copia vecchia.

L'invio è automatico e resiliente:

- debounce breve su ogni modifica, con **retry** a backoff (3 s → max 5 min);
- si riattiva quando il dispositivo torna **online**, quando l'app torna in
  primo piano e ogni minuto mentre è visibile;
- un tentativo best-effort viene fatto anche alla chiusura della pagina
  (`pagehide`, con `keepalive`);
- la card mostra quante **modifiche restano da inviare** e l'esito dell'ultimo
  tentativo.

La logica di merge per record resta la stessa di sempre (è estratta in
`SYNC-CORE` e coperta dai test): cambia solo il trasporto, da blob intero a delta
incrementali + SSE. I dati restano anche in **LocalStorage**, che fa da cache
locale sul dispositivo.

### MCP (per gli agenti)

Lo stesso processo espone un server **MCP** (Model Context Protocol, Streamable
HTTP) su `POST /mcp`, così un agente può **ispezionare e ampliare** il database
alimenti, il programma allenamenti e il diario alimentare. Niente secondo
processo né dipendenze aggiuntive per il protocollo: è JSON-RPC 2.0 scritto a
mano (l'unica dipendenza del server è il driver SQLite puro-Go).

| Tool | Cosa fa |
|---|---|
| `tracker_summary` | Conteggi, sessioni, categorie, ultime registrazioni, target |
| `list_foods` | Elenca e cerca i cibi (per nome e categoria) |
| `add_food` | Aggiunge o aggiorna un alimento (upsert per nome) |
| `list_workouts` | Elenca gli esercizi (filtro per sessione/nome) |
| `add_workout` | Aggiunge o aggiorna un esercizio (upsert per sessione+nome) |
| `list_diary` | Legge il diario: un giorno completo o l'elenco dei giorni recenti |
| `add_diary_item` | Aggiunge un alimento a un giorno (macro calcolate dai grammi) |
| `update_diary_item` | Cambia i grammi di una voce e ricalcola le macro |
| `remove_diary_item` | Rimuove una voce dal giorno |
| `set_diary_type` | Segna il giorno come allenamento o riposo |

Le scritture MCP agiscono direttamente sui **record** in SQLite: toccano solo le
righe interessate (un alimento, un esercizio, un giorno di diario) e ne
pubblicano il cambio via SSE, così gli altri client si allineano subito senza
aspettare il prossimo sync.

Configurazione in un client MCP (esempio con [pi](https://pi.dev)):

```json
{
  "mcpServers": {
    "tracker": { "url": "http://tracker.lab.heiz.cc/mcp" }
  }
}
```

Da riga di comando:

```bash
pi mcp add tracker --url http://localhost:8080/mcp
```

Ogni tool accetta un parametro opzionale `key` per scegliere il blob di sync
(default `default`).

### Sviluppo

```bash
make run      # :8080, serve ./index.html, blob in ./data
make test     # test Go + JS (merge/sync per-record)
make test-js  # solo i test JS del sync
make image REGISTRY=registry.lab.heiz.cc   # build + push (linux/arm64)
```

### Homelab

Lo stack per l'homelab è in `~/code/nix` → [`hosts/lab/docker/tracker/`](../nix/hosts/lab/docker/tracker)
(Traefik + registry privato, `tracker.lab.heiz.cc`).

## 🔄 Alternativa senza server: backup JSON

Se non vuoi self-hostare, in fondo all'app ci sono **Esporta backup (.json)** e
**Importa backup**: sposti il file con iCloud Drive / Google Drive / AirDrop /
WhatsApp. Contiene peso/calorie, diario, database cibi e programma allenamenti.

## Prossimi passi possibili

- [x] Tracciamento macro giornalieri (proteine, carboidrati, grassi)
- [ ] Promemoria settimanale per registrare peso e calorie
- [ ] Export dei dati (peso, calorie, diario) in formato CSV
