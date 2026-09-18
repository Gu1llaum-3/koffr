# ARCHITECTURE — {{PROJET}}

Vue d'ensemble de la structure et des règles qui la tiennent. Le pourquoi est dans `docs/adr/`.
Rempli au lot 0 après l'ADR de stack ; relu à chaque rétro. Ce fichier décrit **ce qui est**, pas
ce qu'on aimerait : une règle écrite ici est vérifiée par le lint ou n'y est pas.

## Vue d'ensemble

{{Un schéma texte : d'où viennent les requêtes, par quelles couches elles passent, où elles
écrivent. Trois à six lignes.}}

```
Client ─► routes/ (adaptateurs) ─► domain/<module>/service ─► persistance
```

## Arborescence

{{L'arbre commenté, un commentaire par dossier significatif. Les fichiers partagés du domaine
(argent, dates, erreurs typées, contexte d'appel, droits) sont nommés ici.}}

```
src/
  env.ts          # configuration d'environnement, validée au démarrage ; seul lecteur des variables
  lib/
    domain/       # métier pur : aucun import du framework, de server/ ni de routes/
      <module>/
        schema    # tables
        model     # entrées, sorties, invariants
        service   # cas d'usage, transactions, vérification des droits
        rules.md  # règles métier tracées (gabarit docs/modeles/rules.md)
      _shared/
    server/       # accès base, auth, jobs, intégrations (porte de sortie unique)
    components/
  routes/         # adaptateurs fins : validation de forme, appel d'un service, rendu
docs/
```

## Règles de dépendance (vérifiées par le lint, bloquantes)

| Depuis            | Interdit d'importer                                  |
| ----------------- | ---------------------------------------------------- |
| `lib/domain/**`   | le framework, `lib/server/**`, `routes/**`, `lib/components/**` |
| `routes/**`       | la couche de persistance (passer par un service)     |
| `lib/domain/<a>`  | `lib/domain/<b>` sauf via son `index`                |
| `src/**` sauf la porte de sortie et les tests | tout client réseau (`fetch`, mail, FTP…) |
| `src/**` sauf `env.ts` | les variables d'environnement brutes            |

## Modules

Une donnée appartient à un seul module. Le code `MOD` préfixe les règles de son `rules.md`.

Proposés par ADR-0010 (statut **proposé** : rien n'est codé dessus). Les modules du domaine vivent
dans `internal/domain/` ; `engine`, `store`, `pipeline`, `state`, `egress` et `httpd` sont des
adaptateurs et n'ont pas de `rules.md`.

| Module      | Code | Contenu                                                                   | Exigences principales |
| ----------- | ---- | ------------------------------------------------------------------------- | --------------------- |
| `config`    | CFG  | analyse stricte du YAML, formes `*_env` / `*_file`, validation au démarrage | `E-032`…`E-037` |
| `resolve`   | RSV  | énumération des candidats, version exécutée, matrice de compatibilité, installation gérée | `E-038`…`E-050`, `E-130`, `E-131` |
| `backup`    | BKP  | verrou par base, espace disque, format, politique de tampon, manifeste     | `E-024`, `E-025`, `E-029`, `E-030`, `E-051`…`E-061` |
| `verify`    | VRF  | empreinte, relecture de structure, revérification périodique               | `E-008`, `E-062`…`E-065` |
| `catalog`   | CAT  | index des archives et des emplacements, état par destination               | `E-028`, `E-057`, `E-068`, `E-070` |
| `retention` | RET  | politique grand-père/père/fils, règles de sûreté, journal des suppressions | `E-077`…`E-081` |
| `restore`   | RST  | contrôles préalables, modes de nettoyage, cible alternative                | `E-082`…`E-087` |
| `schedule`  | SCH  | échéances persistées, absence de rafale, plancher opposable                | `E-088`…`E-091` |
| `alert`     | ALR  | neuf événements, anti-répétition, rétablissement, délégation et repli      | `E-010`, `E-092`…`E-097` |
| `crypto`    | CRY  | destinataires `age`, séquestre, règles de clé                              | `E-072`…`E-076`, `E-132` |
| `uplink`    | UPL  | poussée d'état, validation stricte des réponses, file locale               | `E-017`, `E-105`…`E-111` |

Le reste de ce fichier (vue d'ensemble, arborescence, flux, auth, jobs, intégrations, déploiement)
se remplit au **lot 0**, une fois les ADR acceptés et la première traversée de couches écrite.

## Flux d'une mutation

1. La route valide la **forme** de l'entrée et construit le contexte d'appel (acteur, identifiant
   de requête).
2. Elle appelle un cas d'usage du domaine.
3. Le cas d'usage vérifie les **droits**, ouvre la transaction, vérifie le verrou optimiste,
   applique la règle, écrit.
4. L'audit est alimenté par la persistance, pas par du code applicatif dispersé.
5. Les erreurs métier typées remontent ; la route les traduit pour l'utilisateur.

## Auth et droits

{{Fournisseur d'identité, sessions, modèle de rôles `module:action`, comptes de service.
Vérification des droits dans les services, jamais dans les routes.}}

## Jobs et traitements différés

{{Orchestrateur, idempotence (clé de déduplication), reprises, file de rebut, écran d'administration.
Les agrégats se calculent dans la base, pas en boucles applicatives.}}

## Intégrations

{{Adaptateurs isolés, tous derrière la porte de sortie : mode « puits » hors production, liste
d'hôtes autorisés, journal des échanges. Endpoints entrants derrière une clé par système tiers.}}

## Tests

- Domaine : un test par règle de `rules.md`, sur une base réelle si le module écrit.
- Composants : tests de rendu et d'interaction.
- Parcours : les 10 à 15 parcours critiques, de bout en bout.
- Contrat : la spécification d'API générée est comparée à la version commitée.

## Déploiement

{{Conteneurs, image taguée par SHA, `/healthz`, `/readyz`, variables d'environnement requises.}}
