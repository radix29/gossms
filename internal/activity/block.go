package activity

// BlockProc is the blocking-chain procedure behind the Block tab: one row per
// session with the session blocking it; a recursive CTE walks that into the
// indented tree.
//
// The blocker follows gosmo's sessionBlockerJoin (RequestActivity,
// BlockedRequests, ActiveSessions): the longest-waiting
// sys.dm_os_waiting_tasks task blocked by another session, else
// dm_exec_requests.blocking_session_id. A parallel request waits on its
// workers (CXCONSUMER, blocking_session_id 0) while the lock wait sits on a
// worker.
//
// Never read sys.sysprocesses: that worker is an ecid > 0 row with a zero
// sql_handle, so CROSS APPLYing the text drops it, the parallel reader shows as
// unblocked and its idle head blocker is filtered out. Rejoining by spid also
// repeats every tree row once per thread.
var BlockProc = &Proc{
	MasterName: "sp_block",
	TempDBName: "usp_block",
	script: func(name string) string {
		return "create or alter procedure dbo." + name + blockProcBody
	},
}

// blockProcBody is everything after the procedure name.
const blockProcBody = ` as
begin

    drop table if exists #blkchain;
    drop table if exists #result;

    -- One row per session. An idle session has no request; its text is the
    -- connection's most recent batch.
    select
         s.session_id as spid
        ,coalesce(wb.blocking_session_id, nullif(r.blocking_session_id, r.session_id), 0) as blocked
        ,s.is_user_process
        ,isnull(coalesce(wb.wait_type, r.wait_type), N'') as wait_type
        ,isnull(rtrim(s.host_name), N'') as host_name
        ,isnull(coalesce(wb.resource_description, r.wait_resource), N'') as wait_resource
        ,isnull(r.command, N'AWAITING COMMAND') as command
        ,coalesce(wb.wait_duration_ms, r.wait_time, 0) as wait_time
        ,st.text as sql_text
    into #blkchain
    from
        sys.dm_exec_sessions s
        left join sys.dm_exec_requests r
            on r.session_id = s.session_id
        left join (
            select   session_id, blocking_session_id, wait_type, wait_duration_ms, resource_description
                    ,row_number() over (partition by session_id
                                        order by wait_duration_ms desc, blocking_session_id) as rn
            from     sys.dm_os_waiting_tasks
            where    blocking_session_id > 0
              and    blocking_session_id <> session_id
        ) wb
            on wb.session_id = r.session_id and wb.rn = 1
        outer apply (
            select top (1) c.most_recent_sql_handle
            from   sys.dm_exec_connections c
            where  c.session_id = s.session_id
              and  c.parent_connection_id is null
        ) mc
        outer apply sys.dm_exec_sql_text(isnull(r.sql_handle, mc.most_recent_sql_handle)) st;


    -- A root is a session no other session blocks; a negative blocker
    -- (-2 orphaned DTC, -4 latch owner) is not a session, so it is a root too.
    ;with blktree(spid, blocking_spid, [level])    as
    (
        select   blc.spid
                ,blc.blocked
                ,cast (replicate ('0', 4-len (cast (blc.spid as varchar))) + cast (blc.spid as varchar)+ '#' as varchar (1000)) as [level]
        from    #blkchain blc
        where   blc.blocked <= 0

        union all

        select   blc.spid
                ,blc.blocked
                ,cast(bt.[level] + right (cast ((1000 + blc.spid) as varchar (100)), 4) as varchar (1000)) as [level]
        from    #blkchain as blc
            inner join blktree bt
                on    blc.blocked = bt.spid
        where   blc.blocked > 0
    )
    select
        N'' + isnull(replicate (N'|         ', len (level)/4 - 2),'')
            + case when (len(level)/4 - 1) = 0 then '' else '|------  ' end
            + cast (bt.spid as nvarchar (10)) as blktree
            ,blc.wait_type             as [type]
            ,blc.host_name             as [hostname]
            ,try_cast('<?query --' + char(13) + char(10) + blc.sql_text + char(13) + char(10) + '--?>'  as xml)  as [sql text]
            ,blc.wait_resource         as [wait resource]
            ,blc.command               as [command]
            ,blc.wait_time             as [waittime]
            ,blocking_spid
            ,left([level],charindex('#',[level],1)-1) as root
            ,level
    into #result
    from blktree bt
        inner join #blkchain blc on blc.spid = bt.spid
    where
        -- An idle session in no chain is noise, and so is a system session
        -- in none.
        not (
            blc.command = N'AWAITING COMMAND' and
            blocking_spid  = 0 and
            not exists (select 1 from #blkchain blc3 where bt.spid = blc3.blocked)
        )
        -- "!=", and no less-than or ampersand from here on: this text is
        -- the [sql text] of the session running it, and past the PI close
        -- above either is markup that fails the xml cast, blanking that row.
        and (
            blc.is_user_process = 1 or
            blocking_spid != 0 or
            exists (select 1 from #blkchain blc4 where bt.spid = blc4.blocked)
        )
    order by level asc


    select blktree, type, hostname, [sql text], [wait resource], command, waittime, blocking_spid
    from #result order by level


end`
