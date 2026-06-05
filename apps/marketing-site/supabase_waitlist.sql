create table if not exists public.waitlist (
  id uuid primary key default gen_random_uuid(),
  created_at timestamptz not null default now(),
  email text not null,
  company text,
  role text,
  data_source text,
  source text,
  page text default 'groundwork_marketing'
);

alter table public.waitlist enable row level security;

drop policy if exists "allow public waitlist inserts" on public.waitlist;
create policy "allow public waitlist inserts"
on public.waitlist
for insert
to anon
with check (
  email is not null
  and length(email) >= 5
  and position('@' in email) > 1
);

drop policy if exists "deny public waitlist reads" on public.waitlist;
create policy "deny public waitlist reads"
on public.waitlist
for select
to anon
using (false);
