### ✨ Features                                                                                                                                 
      2                                                                                                                                                  
      3 +* **Phase 58 — Interactive Security Access Map**: New "Access Map" tab in SecurityPage (admin-only). Renders the full RBAC graph — Users 
        +→ Roles → Privileges → Content Selectors — as a plain SVG with hierarchical columns. Type pills + searchable combobox let you pick a star       
        +ting node; the graph then shows only that node's chain (upstream and downstream). By default, the chains for `admin` and `anonymous` are        
        +pre-rendered so the tab is useful on first open. Selecting any node in the SVG updates the selection. A sidebar shows type-specific detai       
        +ls (user status/source/roles, role privilege count, privilege CS link, CS expression). Backend: new `GET /api/v1/security/access-graph` h       
        +andler (admin-only) returns all four entity collections in one response; graph joins are resolved client-side.

### 🐛 Bug Fixes
      10  
      11 +* **Access Map renders only chain nodes**: SVG now renders only nodes that belong to the selected chain instead of all nodes at dim opac
         +ity. SVG element count is proportional to the chain size, keeping the graph fast and readable even on large deployments.                
      12 +* **Create local user fails with "invalid input: username is required"**: The create-user form was sending `{ username: "…" }` but `doma
         +in.User.Username` deserializes from `json:"userId"` (Nexus API compat). Fixed by aligning the form state key to `userId`.