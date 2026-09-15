import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router";
import { api, post, type Membership, type Principal } from "../api";

/* The seats of the signed-in account (#262). One query for the shell's
   switcher and for the page a session without an active seat lands on. */
export function useMemberships() {
  return useQuery({
    queryKey: ["memberships"],
    queryFn: () => api<Membership[]>("/auth/memberships"),
    staleTime: 60_000,
  });
}

/* Switching rewrites the session's active seat on the server. Everything the
   client holds belongs to the organisation just left, so the cache goes
   entirely and the page returns to the start: an address like /agents/<id>
   names an object of the other organisation. onSwitched reloads the principal. */
export function useSwitchOrg(onSwitched: () => void) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  return useMutation({
    mutationFn: (orgId: string) => post<Principal>("/auth/switch-org", { org_id: orgId }),
    onSuccess: () => {
      qc.clear();
      navigate("/", { replace: true });
      onSwitched();
    },
  });
}
